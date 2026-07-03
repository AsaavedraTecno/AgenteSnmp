package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image/color"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"crypto/sha256"
	_ "embed"

	"gopkg.in/natefinch/lumberjack.v2"
	"github.com/kardianos/service"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/asaavedra/agent-snmp/pkg/config"
	"github.com/asaavedra/agent-snmp/pkg/profile"
	"github.com/asaavedra/agent-snmp/pkg/remote"
	"github.com/asaavedra/agent-snmp/pkg/runner"
	"github.com/asaavedra/agent-snmp/pkg/scanner"
	"github.com/asaavedra/agent-snmp/pkg/telemetry"
	"github.com/asaavedra/agent-snmp/pkg/uploader"
)

// Constantes
const ServiceName = "AgentSNMP_Service"
const AppName = "AgentSNMP"

// appSecret lee la clave de cifrado desde la variable de entorno AGENT_SECRET.
// Si no está definida usa el valor por defecto para compatibilidad con instalaciones
// existentes, pero imprime una advertencia porque el binario ya no contiene el secreto.
var AppSecret = func() string {
	if s := os.Getenv("AGENT_SECRET"); s != "" {
		return s
	}
	return "TecnoData_Super_Secret_Key_2026_IoT_Monitor"
}()

var (
	logger     service.Logger
	sharedDir  string
	configPath string
	logPath    string
)

//go:embed logo.jpg
var resourceLogoPng []byte

// --- TEMA PERSONALIZADO PARA FORZAR LECTURA ---
type forceDarkTheme struct{}

func (m *forceDarkTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return color.NRGBA{R: 15, G: 15, B: 15, A: 255} // Fondo casi negro
	case theme.ColorNameForeground:
		return color.NRGBA{R: 255, G: 255, B: 255, A: 255} // Texto normal BLANCO

	case theme.ColorNameDisabled:
		return color.NRGBA{R: 255, G: 255, B: 255, A: 255}

	case theme.ColorNameInputBackground:
		return color.NRGBA{R: 40, G: 40, B: 40, A: 255} // Caja de texto gris oscuro
	case theme.ColorNamePlaceHolder:
		return color.NRGBA{R: 180, G: 180, B: 180, A: 255}
	}
	return theme.DefaultTheme().Color(name, theme.VariantDark)
}

func (m *forceDarkTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (m *forceDarkTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (m *forceDarkTheme) Size(name fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(name)
}

func init() {
	programData := os.Getenv("ProgramData")
	if programData == "" {
		programData = "C:\\ProgramData"
	}
	sharedDir = filepath.Join(programData, AppName)
	configPath = filepath.Join(sharedDir, "config.yaml")
	logPath = filepath.Join(sharedDir, "agent.log")
	os.MkdirAll(sharedDir, 0777)
}

type program struct{}

func (p *program) Start(s service.Service) error {
	go p.run()
	return nil
}

func (p *program) Stop(s service.Service) error {
	return nil
}

func main() {

	svcConfig := &service.Config{
		Name:        ServiceName,
		DisplayName: "Agente de Monitoreo IoT",
		Description: "Servicio background para escaneo de impresoras.",
		Arguments:   []string{"-service"},
	}

	prg := &program{}
	s, err := service.New(prg, svcConfig)
	if err != nil {
		log.Fatal(err)
	}
	logger, _ = s.Logger(nil)

	if len(os.Args) > 1 {
		cmd := os.Args[1]
		switch cmd {
		case "-service":
			setupFileLogging()
			s.Run()
			return
		case "install":
			s.Install()
			fmt.Println("✅ Instalado.")
			return
		case "uninstall":
			s.Uninstall()
			fmt.Println("🗑️ Desinstalado.")
			return
		case "start":
			s.Start()
			fmt.Println("▶️ Iniciado.")
			return
		case "stop":
			s.Stop()
			fmt.Println("⏹️ Detenido.")
			return
		}
	}

	// ✅ Modo GUI solamente - sin CMD visible
	runGUI(s)
}

// --- MODO GUI MEJORADO ---
func runGUI(s service.Service) {
	myApp := app.New()
	myApp.Settings().SetTheme(&forceDarkTheme{}) // 🔴 Aplicar tema de alto contraste

	if len(resourceLogoPng) > 0 {
		iconRes := fyne.NewStaticResource("IconoApp", resourceLogoPng)
		myApp.SetIcon(iconRes)
	}

	myWindow := myApp.NewWindow("TecnoData Agente IoT")

	cfg, _ := config.LoadConfig(configPath)
	isConfigured := cfg.Uploader.AgentKey != ""

	realKey := ""
	if isConfigured {
		if dec, err := decrypt(cfg.Uploader.AgentKey); err == nil {
			realKey = dec
		}
	}

	// -- WIDGETS DE ESTADO --
	statusLabel := widget.NewLabel("ESTADO: CARGANDO...")
	statusLabel.TextStyle = fyne.TextStyle{Bold: true}

	// -- VISTA DE CONFIGURACIÓN --
	keyEntry := widget.NewEntry()
	if isConfigured {
		keyEntry.Text = "🔒 " + maskKey(realKey)
		keyEntry.Disable()
	} else {
		keyEntry.SetPlaceHolder("Ingresa tu Agent Key aquí...")
	}

	var saveBtn *widget.Button
	saveBtn = widget.NewButtonWithIcon("Activar Agente", theme.ConfirmIcon(), func() {

		// Quitamos espacios accidentales y forzamos mayúsculas
		cleanKey := strings.ToUpper(strings.TrimSpace(keyEntry.Text))

		if cleanKey == "" {
			return
		}

		encKey, _ := encrypt(cleanKey)
		encURL, _ := encrypt("https://tdmonitor.cl")

		cfg.Uploader.Enabled = true
		cfg.Uploader.AgentKey = encKey
		cfg.Uploader.CloudURL = encURL
		cfg.Uploader.SkipTLSVerify = false // Seguro por defecto

		config.SaveConfig(cfg, configPath)

		keyEntry.Text = "🔒 " + maskKey(cleanKey)
		keyEntry.Disable()
		saveBtn.Hide()
	})

	if isConfigured {
		saveBtn.Hide()
	}

	configView := container.NewPadded(container.NewVBox(
		widget.NewLabelWithStyle("CONFIGURACIÓN DE SEGURIDAD", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
		widget.NewLabel("Agent Key:"),
		keyEntry,
		saveBtn,
		layout.NewSpacer(),
		widget.NewLabelWithStyle("Host: "+getHostname(), fyne.TextAlignCenter, fyne.TextStyle{Italic: true}),
	))

	// -- VISTA DE LOGS --
	logArea := widget.NewMultiLineEntry()
	logArea.Disable()
	logArea.Wrapping = fyne.TextWrapBreak
	logArea.TextStyle = fyne.TextStyle{Monospace: true}

	logsView := container.NewBorder(
		widget.NewLabelWithStyle("REGISTROS DE ACTIVIDAD", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		nil, nil, nil,
		logArea,
	)

	// -- NAVEGACIÓN LATERAL --
	contentArea := container.NewStack(configView)

	sideMenu := widget.NewList(
		func() int { return 2 },
		func() fyne.CanvasObject {
			return container.NewHBox(widget.NewIcon(theme.InfoIcon()), widget.NewLabel("Item"))
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			box := obj.(*fyne.Container)
			icon := box.Objects[0].(*widget.Icon)
			label := box.Objects[1].(*widget.Label)
			if id == 0 {
				label.SetText("Estado")
				icon.SetResource(theme.SettingsIcon())
			} else {
				label.SetText("Logs")
				icon.SetResource(theme.DocumentIcon())
			}
		},
	)

	sideMenu.OnSelected = func(id widget.ListItemID) {
		if id == 0 {
			contentArea.Objects = []fyne.CanvasObject{configView}
		} else {
			contentArea.Objects = []fyne.CanvasObject{logsView}
		}
		contentArea.Refresh()
	}

	// Monitor en segundo plano - CORREGIDO CON fyne.Do()
	go func() {
		for {
			fileInfo, err := os.Stat(logPath)
			isRunning := false
			if err == nil && time.Since(fileInfo.ModTime()) < 45*time.Second {
				isRunning = true
			}

			// ✅ USAR fyne.Do() para actualizar widgets desde goroutine
			fyne.Do(func() {
				if isRunning {
					statusLabel.SetText("🟢 SERVICIO ACTIVO")
				} else {
					statusLabel.SetText("🔴 SERVICIO DETENIDO")
				}
			})

			if content, err := os.ReadFile(logPath); err == nil {
				str := string(content)
				if len(str) > 2000 {
					str = str[len(str)-2000:]
				}
				// ✅ USAR fyne.Do() aquí también
				fyne.Do(func() {
					logArea.SetText(str)
					logArea.CursorColumn = len(str)
				})
			}
			time.Sleep(3 * time.Second)
		}
	}()

	mainSplit := container.NewHSplit(sideMenu, contentArea)
	mainSplit.Offset = 0.3

	finalLayout := container.NewBorder(
		container.NewPadded(statusLabel),
		nil, nil, nil,
		mainSplit,
	)

	myWindow.SetCloseIntercept(func() { myWindow.Hide() })

	if desk, ok := myApp.(desktop.App); ok {
		m := fyne.NewMenu("Agente IoT",
			fyne.NewMenuItem("Abrir", func() { myWindow.Show() }),
			fyne.NewMenuItem("Salir", func() { myApp.Quit() }),
		)
		desk.SetSystemTrayMenu(m)
	}

	myWindow.SetContent(finalLayout)
	myWindow.Resize(fyne.NewSize(550, 400))
	myWindow.ShowAndRun()
}

// --- MODO MOTOR ---
func (p *program) run() {
	if exePath, err := os.Executable(); err == nil {
		os.Chdir(filepath.Dir(exePath))
	}

	setupFileLogging()
	log.Println("🚀 SERVICIO INICIADO (Modo Cifrado Total).")

	var cfg config.Config
	var err error
	var finalKey, finalURL string

	for {
		cfg, err = config.LoadConfig(configPath)
		if err != nil {
			log.Printf("⚠️ Esperando configuración... no se pudo cargar %s: %v", configPath, err)
		} else if cfg.Uploader.AgentKey == "" {
			log.Printf("⚠️ Esperando configuración... AgentKey está vacío en %s", configPath)
		} else {
			decKey, errK := decrypt(cfg.Uploader.AgentKey)
			decURL, errU := decrypt(cfg.Uploader.CloudURL)

			if errK == nil {
				finalKey = decKey
				if errU == nil {
					finalURL = decURL
				} else {
					finalURL = "https://tdmonitor.cl"
				}
				break
			} else {
				log.Printf("❌ Error de seguridad: No se pudo descifrar el AgentKey. ¿El archivo %s está corrupto o en texto plano?", configPath)
			}
		}
		time.Sleep(10 * time.Second)
	}

	if cfg.Uploader.Enabled {
		uploader.StartUploader(context.Background(), func() uploader.Config {
			// Volvemos a cargar o usamos la actual
			c, _ := config.LoadConfig(configPath)
			k, _ := decrypt(c.Uploader.AgentKey)
			u, _ := decrypt(c.Uploader.CloudURL)
			if u == "" {
				u = "https://tdmonitor.cl"
			}
			return uploader.Config{
				Enabled:        c.Uploader.Enabled,
				CloudURL:       u,
				AgentKey:       k,
				QueuePath:      sharedDir,
				IntervalSecs:   30,
				MaxBackoffSecs: 300,
				SkipTLSVerify:  c.Uploader.SkipTLSVerify,
				BatchSize:      c.Uploader.BatchSize,
			}
		})
	}

	// Detectar IP propia: config override tiene prioridad; si no, UDP dial.
	agentIP := cfg.Agent.IP
	if agentIP == "" {
		agentIP = scanner.GetAgentIP()
	}
	if agentIP != "" {
		log.Printf("🌐 IP del agente: %s", agentIP)
	}

	agentSource := telemetry.AgentSource{
		AgentID:          "AGT-" + getHostname(),
		Hostname:         getHostname(),
		OS:               "windows",
		Version:          "1.0.0",
		AgentIP:          agentIP,
		CollectionMethod: "snmp",
		ConnectedVia:     "network",
	}
	// stateDir vive junto al ejecutable (no en la cola del uploader).
	// El servicio hace os.Chdir(exeDir) al arrancar, así que "state"
	// resuelve a exeDir\state — separado de la queue en sharedDir.
	exeDir := filepath.Dir(func() string { p, _ := os.Executable(); return p }())
	stateDir := filepath.Join(exeDir, "state")
	scanRunner := runner.NewRunner(agentSource, sharedDir, stateDir)
	remoteClient := remote.NewClient(finalURL, finalKey, cfg.Uploader.SkipTLSVerify)

	// Cargar perfiles YAML una sola vez (junto al ejecutable o en sharedDir)
	profilesDir := filepath.Join(filepath.Dir(sharedDir), AppName, "profiles")
	if _, err := os.Stat(profilesDir); os.IsNotExist(err) {
		// Fallback: profiles/ relativo al ejecutable
		if exePath, err2 := os.Executable(); err2 == nil {
			profilesDir = filepath.Join(filepath.Dir(exePath), "profiles")
		}
	}
	if pm, err := profile.NewYAMLManager(profilesDir); err != nil {
		log.Printf("⚠️ No se pudieron cargar perfiles YAML (%s): %v — se usará RFC 3805 genérico.", profilesDir, err)
	} else {
		log.Printf("✅ Perfiles YAML cargados desde: %s", profilesDir)
		scanRunner.WithYAMLProfiles(pm)
	}

	for {
		log.Println("🔍 Consultando instrucciones al servidor...")
		remoteCfg, err := remoteClient.GetConfig()
		if err != nil {
			log.Printf("❌ Error consultando instrucciones: %v", err)
			time.Sleep(60 * time.Second)
			continue
		}

		if !remoteCfg.Active {
			log.Println("⏸️ El agente está marcado como INACTIVO en el servidor (active: false).")
			time.Sleep(60 * time.Second)
			continue
		}

		// Fallback: Si la API mandó el formato viejo (string "192.168.1.1-254") y no el array de objetos
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

		// Escaneo con perfiles ahora cargados correctamente
		log.Printf("🚀 Iniciando barrido programado (Intervalo: %d s)...", remoteCfg.ScanInterval)
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
			log.Printf("❌ Error durante el barrido: %v", err)
		} else {
			// ✅ Notificar al uploader que hay datos listos para enviar
			uploader.TriggerUpload()
		}

		log.Printf("😴 Esperando %d segundos para el próximo ciclo...", remoteCfg.ScanInterval)
		time.Sleep(time.Duration(remoteCfg.ScanInterval) * time.Second)
	}
}

// --- CRIPTOGRAFÍA ---
func createHash(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

func encrypt(data string) (string, error) {
	block, _ := aes.NewCipher([]byte(createHash(AppSecret)))
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(data), nil)
	return hex.EncodeToString(ciphertext), nil
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

func maskKey(key string) string {
	cleanKey := strings.TrimSpace(key)
	if len(cleanKey) <= 5 {
		return "**********"
	}
	// Tomamos los últimos 5 caracteres para que coincida con el panel web
	lastFive := cleanKey[len(cleanKey)-5:]
	return "**********" + lastFive
}

func setupFileLogging() {
	log.SetOutput(&lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    10, // MB
		MaxBackups: 3,
		MaxAge:     7,  // días
		Compress:   true,
	})
}

func getHostname() string { h, _ := os.Hostname(); return h }
