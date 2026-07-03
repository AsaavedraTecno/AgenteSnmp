//go:build ignore

package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/asaavedra/agent-snmp/pkg/config"
	"github.com/asaavedra/agent-snmp/pkg/profile"
	"github.com/asaavedra/agent-snmp/pkg/remote"
	"github.com/asaavedra/agent-snmp/pkg/runner"
	"github.com/asaavedra/agent-snmp/pkg/scanner"
	"github.com/asaavedra/agent-snmp/pkg/telemetry"
	"github.com/asaavedra/agent-snmp/pkg/uploader"
)

var AppSecret = "TecnoData_Super_Secret_Key_2026_IoT_Monitor"

func main() {
	// --- 1. CONFIGURACIÓN INICIAL ---
	projectRoot := findProjectRoot()
	if err := os.Chdir(projectRoot); err != nil {
		log.Fatalf("❌ No se pudo cambiar al directorio raíz: %v", err)
	}

	configPath := filepath.Join(projectRoot, "config.yaml")
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("❌ Error cargando config: %v", err)
	}

	log.SetOutput(os.Stdout)
	log.SetFlags(log.Ltime | log.Lshortfile)

	queuePath := cfg.Sinks.File.Path
	if queuePath == "" {
		queuePath = "./queue"
	}
	os.MkdirAll(queuePath, 0755)
	os.MkdirAll("./profiles", 0755)
	os.MkdirAll("./state", 0755)

	// --- 1.5 DESENCRIPTAR LLAVE IGUAL QUE EN PRODUCCIÓN ---
	realKey := cfg.Uploader.AgentKey
	realURL := cfg.Uploader.CloudURL

	if decURL, err := decrypt(cfg.Uploader.CloudURL); err == nil {
		realURL = decURL
	}
	if decKey, err := decrypt(cfg.Uploader.AgentKey); err == nil {
		realKey = decKey
		fmt.Println("🔓 Llave desencriptada correctamente para la prueba.")
	} else {
		fmt.Println("⚠️ Usando la llave como texto plano (no estaba encriptada).")
	}

	fmt.Println("✅ Config cargada:")
	fmt.Printf("   Cloud URL : %s\n", realURL)
	if len(realKey) > 10 {
		fmt.Printf("   Agent Key : %s...\n", realKey[:10])
	} else {
		fmt.Printf("   Agent Key : %s\n", realKey)
	}

	// --- 2. CONECTAR A LA API (Igual que en producción) ---
	fmt.Println("\n🌐 Conectando a la API de Laravel...")
	remoteClient := remote.NewClient(realURL, realKey, cfg.Uploader.SkipTLSVerify)
	remoteCfg, err := remoteClient.GetConfig()

	if err != nil {
		log.Fatalf("❌ Error consultando la API: %v", err)
	}

	if !remoteCfg.Active {
		log.Fatalf("⚠️ La API indica que el agente NO está activo o configurado. Saliendo.")
	}

	// Detectar IP propia: config override tiene prioridad; si no, UDP dial.
	agentIP := cfg.Agent.IP
	if agentIP == "" {
		agentIP = scanner.GetAgentIP()
	}
	if agentIP != "" {
		fmt.Printf("🌐 IP del agente detectada: %s\n", agentIP)
	}

	// Fallback 1: Si la API mandó el formato viejo (string) y no el array nuevo
	rangesToProcess := remoteCfg.IPRanges
	if len(rangesToProcess) == 0 && remoteCfg.IPRange != "" {
		parts := strings.Split(remoteCfg.IPRange, "-")
		if len(parts) == 2 {
			rangesToProcess = append(rangesToProcess, scanner.IPRangeConfig{
				IPFrom: strings.TrimSpace(parts[0]),
				IPTo:   strings.TrimSpace(parts[1]),
				Active: true,
			})
		}
	}

	// Fallback 2: Sin rangos en la API → usar la red del agente automáticamente
	if len(rangesToProcess) == 0 && agentIP != "" {
		if r := scanner.AgentIPToRange(agentIP); r != nil {
			rangesToProcess = []scanner.IPRangeConfig{*r}
			fmt.Printf("⚠️  Sin rangos de API — usando red del agente: %s → %s\n", r.IPFrom, r.IPTo)
		}
	}

	if len(rangesToProcess) == 0 {
		log.Fatalf("⚠️ La API no envió rangos y la autodetección de red falló.")
	}

	fmt.Printf("✅ API respondió OK. Se recibieron %d rangos.\n", len(rangesToProcess))
	for i, r := range rangesToProcess {
		fmt.Printf("   -> Rango %d: %s al %s (Activo: %t)\n", i+1, r.IPFrom, r.IPTo, r.Active)
	}

	// --- 3. EJECUTAR ESCANEO ---
	agentSource := telemetry.AgentSource{
		AgentID:          "DEV-LOCAL",
		Hostname:         "development",
		OS:               "windows",
		Version:          "dev",
		AgentIP:          agentIP,
		CollectionMethod: "snmp",
		ConnectedVia:     "network",
	}

	scanRunner := runner.NewRunner(agentSource, queuePath, filepath.Join(projectRoot, "state"))

	// Inyectar motor de perfiles YAML (cargado una sola vez aquí)
	profilesDir := filepath.Join(projectRoot, "profiles")
	pm, err := profile.NewYAMLManager(profilesDir)
	if err != nil {
		log.Printf("⚠️ No se pudieron cargar perfiles YAML (%s): %v — se usará perfil genérico.", profilesDir, err)
	} else {
		fmt.Printf("✅ Perfiles YAML cargados desde: %s\n", profilesDir)
		scanRunner.WithYAMLProfiles(pm)
	}

	fmt.Printf("\n🚀 Iniciando scan con datos de la API...\n")

	err = scanRunner.Run(context.Background(), runner.JobParams{
		IPRanges:      rangesToProcess,
		AgentIP:       agentIP,
		Community:     remoteCfg.Community,
		SNMPVersion:   remoteCfg.Version,
		SNMPPort:      161,
		Timeout:       3000 * time.Millisecond,
		Retries:       2,
		MaxConcurrent: remoteCfg.MaxConcurrent,
		DelayBetween:  50 * time.Millisecond,
	})

	if err != nil {
		log.Fatalf("❌ Error en scan: %v", err)
	}

	fmt.Printf("\n✅ Scan completado. Archivos en cola: %s\n", queuePath)

	// --- 4. SUBIDA MANUAL EN BATCH ---
	fmt.Println("\n☁️  Iniciando subida manual de datos en modo BATCH...")
	
	upCfg := uploader.Config{
		Enabled:       true,
		CloudURL:      realURL,
		AgentKey:      realKey,
		QueuePath:     queuePath,
		SkipTLSVerify: cfg.Uploader.SkipTLSVerify,
		BatchSize:     cfg.Uploader.BatchSize,
	}
	
	if upCfg.BatchSize <= 0 {
		upCfg.BatchSize = 50
	}
	
	success := uploader.Upload(upCfg)
	if !success {
		log.Printf("❌ Hubo errores al subir datos batch.")
	} else {
		fmt.Println("✨ Todo enviado exitosamente en batch y cola limpia.")
	}
}


func findProjectRoot() string {
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		for i := 0; i < 5; i++ {
			if fileExists(filepath.Join(dir, "config.yaml")) {
				return dir
			}
			dir = filepath.Dir(dir)
		}
	}
	_, filename, _, ok := runtime.Caller(0)
	if ok {
		dir := filepath.Dir(filename)
		for i := 0; i < 5; i++ {
			if fileExists(filepath.Join(dir, "config.yaml")) {
				return dir
			}
			dir = filepath.Dir(dir)
		}
	}
	wd, _ := os.Getwd()
	return wd
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func createHash(key string) string {
	hasher := md5.New()
	hasher.Write([]byte(key))
	return hex.EncodeToString(hasher.Sum(nil))
}

func decrypt(data string) (string, error) {
	key := []byte(createHash(AppSecret))
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	dataBytes, _ := hex.DecodeString(data)
	if len(dataBytes) < nonceSize {
		return "", fmt.Errorf("short")
	}
	nonce, ciphertext := dataBytes[:nonceSize], dataBytes[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
