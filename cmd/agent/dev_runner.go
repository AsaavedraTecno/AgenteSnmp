//go:build ignore

package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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
	remoteClient := remote.NewClient(realURL, realKey)
	remoteCfg, err := remoteClient.GetConfig()

	if err != nil {
		log.Fatalf("❌ Error consultando la API: %v", err)
	}

	if !remoteCfg.Active {
		log.Fatalf("⚠️ La API indica que el agente NO está activo o configurado. Saliendo.")
	}

	// Fallback: Si la API mandó el formato viejo y no el array nuevo
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

	if len(rangesToProcess) == 0 {
		log.Fatalf("⚠️ La API respondió pero no envió rangos válidos. ¿Actualizaste el Controller en Laravel?")
	}

	fmt.Printf("✅ API respondió OK. Se recibieron %d rangos.\n", len(rangesToProcess))
	for i, r := range rangesToProcess {
		fmt.Printf("   -> Rango %d: %s al %s (Activo: %t)\n", i+1, r.IPFrom, r.IPTo, r.Active)
	}

	// --- 3. EJECUTAR ESCANEO ---
	agentSource := telemetry.AgentSource{
		AgentID:  "DEV-LOCAL",
		Hostname: "development",
		OS:       "windows",
		Version:  "dev",
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
		IPRanges:      rangesToProcess, // PASAMOS LOS RANGOS DE LA API
		Community:     remoteCfg.Community,
		SNMPVersion:   remoteCfg.Version,
		SNMPPort:      161,
		Timeout:       3000 * time.Millisecond, // colector aplica mín 3s de todas formas
		Retries:       2,
		MaxConcurrent: remoteCfg.MaxConcurrent,
		DelayBetween:  50 * time.Millisecond,
	})

	if err != nil {
		log.Fatalf("❌ Error en scan: %v", err)
	}

	fmt.Printf("\n✅ Scan completado. Archivos en cola: %s\n", queuePath)

	// --- 4. SUBIDA MANUAL ---
	fmt.Println("\n☁️  Iniciando subida manual de datos...")
	if err := manualUpload(realURL, realKey, queuePath); err != nil {
		log.Printf("❌ Error al subir datos: %v", err)
	} else {
		fmt.Println("✨ Todo enviado exitosamente y cola limpia.")
	}
}

// manualUpload replica la lógica de subida SOLO para este script de desarrollo.
func manualUpload(cloudURL, agentKey, queuePath string) error {
	pattern := filepath.Join(queuePath, "*.json")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("error listando archivos: %v", err)
	}

	if len(files) == 0 {
		fmt.Println("📭 Cola vacía, nada que subir.")
		return nil
	}

	fmt.Printf("📤 Encontrados %d archivos para subir...\n", len(files))

	client := &http.Client{Timeout: 30 * time.Second}
	baseURL := strings.TrimRight(cloudURL, "/")
	endpoint := fmt.Sprintf("%s/api/agent/telemetry", baseURL)

	for _, filePath := range files {
		data, err := os.ReadFile(filePath)
		if err != nil {
			log.Printf("⚠️ Error leyendo %s: %v", filepath.Base(filePath), err)
			continue
		}

		var content interface{}
		if err := json.Unmarshal(data, &content); err != nil {
			log.Printf("⚠️ JSON inválido en %s: %v", filepath.Base(filePath), err)
			continue
		}

		payload := map[string]interface{}{
			"events": []interface{}{content},
		}

		jsonData, _ := json.Marshal(payload)

		req, err := http.NewRequest("POST", endpoint, bytes.NewReader(jsonData))
		if err != nil {
			return err
		}

		req.Header.Set("X-Agent-Key", agentKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		fmt.Printf("   ➡ Enviando %s... ", filepath.Base(filePath))
		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("❌ Error red: %v\n", err)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			fmt.Printf("✅ OK (%d)\n", resp.StatusCode)
			os.Remove(filePath)
		} else {
			body, _ := io.ReadAll(resp.Body)
			fmt.Printf("⚠️ Rechazado (%d): %s\n", resp.StatusCode, string(body))
		}
	}

	return nil
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
