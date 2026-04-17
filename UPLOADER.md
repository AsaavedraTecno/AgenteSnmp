package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image/color"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "embed"

	"github.com/kardianos/service"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/asaavedra/agent-snmp/pkg/config"
	"github.com/asaavedra/agent-snmp/pkg/remote"
	"github.com/asaavedra/agent-snmp/pkg/runner"
	"github.com/asaavedra/agent-snmp/pkg/telemetry"
	"github.com/asaavedra/agent-snmp/pkg/uploader"
)

// Constantes
const ServiceName = "AgentSNMP_Service"
const AppName = "AgentSNMP"

var AppSecret = "TecnoData_Super_Secret_Key_2026_IoT_Monitor"

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

		if keyEntry.Text == "" {
			return
		}

		encKey, _ := encrypt(keyEntry.Text)
		encURL, _ := encrypt("http://localhost:8000")

		cfg.Uploader.Enabled = true
		cfg.Uploader.AgentKey = encKey
		cfg.Uploader.CloudURL = encURL

		config.SaveConfig(cfg, configPath)

		keyEntry.Text = "🔒 " + maskKey(keyEntry.Text)
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

	// Monitor en segundo plano
	go func() {
		for {
			fileInfo, err := os.Stat(logPath)
			isRunning := false
			if err == nil && time.Since(fileInfo.ModTime()) < 45*time.Second {
				isRunning = true
			}

			if isRunning {
				statusLabel.SetText("🟢 SERVICIO ACTIVO")
			} else {
				statusLabel.SetText("🔴 SERVICIO DETENIDO")
			}
			if content, err := os.ReadFile(logPath); err == nil {
				str := string(content)
				if len(str) > 2000 {
					str = str[len(str)-2000:]
				}
				logArea.SetText(str)
				logArea.CursorColumn = len(str)
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
		if err == nil && cfg.Uploader.AgentKey != "" {
			decKey, errK := decrypt(cfg.Uploader.AgentKey)
			decURL, errU := decrypt(cfg.Uploader.CloudURL)

			if errK == nil {
				finalKey = decKey
				if errU == nil {
					finalURL = decURL
				} else {
					finalURL = "http://localhost:8000"
				}
				break
			}
		}
		time.Sleep(10 * time.Second)
	}

	if cfg.Uploader.Enabled {
		go uploader.StartUploader(uploader.Config{
			Enabled:        true,
			CloudURL:       finalURL,
			AgentKey:       finalKey,
			QueuePath:      sharedDir,
			IntervalSecs:   30,
			MaxBackoffSecs: 300,
		})
	}

	agentSource := telemetry.AgentSource{
		AgentID:  "AGT-" + getHostname(),
		Hostname: getHostname(),
		OS:       "windows",
		Version:  "1.0.0",
	}
	scanRunner := runner.NewRunner(agentSource, sharedDir)
	remoteClient := remote.NewClient(finalURL, finalKey)

	for {
		remoteCfg, err := remoteClient.GetConfig()
		if err == nil && remoteCfg.Active {
			// Escaneo con perfiles ahora cargados correctamente
			scanRunner.Run(context.Background(), runner.JobParams{
				IPRange:       remoteCfg.IPRange,
				Community:     remoteCfg.Community,
				SNMPVersion:   remoteCfg.Version,
				SNMPPort:      161,
				Timeout:       2000 * time.Millisecond,
				Retries:       1,
				MaxConcurrent: remoteCfg.MaxConcurrent,
				DelayBetween:  50 * time.Millisecond,
			})
			time.Sleep(time.Duration(remoteCfg.ScanInterval) * time.Second)
		} else {
			time.Sleep(60 * time.Second)
		}
	}
}

// --- CRIPTOGRAFÍA ---
func createHash(key string) string {
	hasher := md5.New()
	hasher.Write([]byte(key))
	return hex.EncodeToString(hasher.Sum(nil))
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
	if len(key) <= 5 {
		return "****"
	}
	return key[:5] + "****************"
}

func setupFileLogging() {
	f, _ := os.OpenFile(logPath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	log.SetOutput(f)
}
func getHostname() string { h, _ := os.Hostname(); return h }
