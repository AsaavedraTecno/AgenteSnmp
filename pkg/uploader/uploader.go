package uploader

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/asaavedra/agent-snmp/pkg/telemetry"
)

// Config contiene la configuración del uploader
type Config struct {
	Enabled        bool
	CloudURL       string
	AgentKey       string
	IntervalSecs   int
	MaxBackoffSecs int
	QueuePath      string
}

// UploaderState mantiene el estado del uploader para backoff exponencial
type UploaderState struct {
	baseInterval      time.Duration
	currentInterval   time.Duration
	maxBackoff        time.Duration
	backoffMultiplier float64
}

// StartUploader inicia una goroutine que envía eventos periódicamente a la nube
func StartUploader(cfg Config) {
	if !cfg.Enabled {
		log.Println("⚠️  Uploader disabled in config")
		return
	}

	maxBackoff := time.Duration(cfg.MaxBackoffSecs) * time.Second
	if maxBackoff == 0 {
		maxBackoff = 5 * time.Minute // Default 5 minutos
	}

	go func() {
		state := &UploaderState{
			baseInterval:      time.Duration(cfg.IntervalSecs) * time.Second,
			currentInterval:   time.Duration(cfg.IntervalSecs) * time.Second,
			maxBackoff:        maxBackoff,
			backoffMultiplier: 1.0,
		}

		ticker := time.NewTicker(state.currentInterval)
		defer ticker.Stop()

		log.Printf("🚀 Uploader started (base interval: %d s, host: %s)",
			cfg.IntervalSecs, cfg.CloudURL)

		for range ticker.C {
			success := uploadBatch(cfg)

			// Ajustar intervalo (Backoff Exponencial)
			if success {
				if state.backoffMultiplier != 1.0 {
					log.Printf("✅ Backoff reset to base interval (%d seconds)", cfg.IntervalSecs)
					state.backoffMultiplier = 1.0
					state.currentInterval = state.baseInterval
					ticker.Reset(state.currentInterval)
				}
			} else {
				state.backoffMultiplier *= 2.0
				newInterval := time.Duration(float64(state.baseInterval) * state.backoffMultiplier)

				if newInterval > state.maxBackoff {
					newInterval = state.maxBackoff
				}

				state.currentInterval = newInterval
				ticker.Reset(state.currentInterval)
				log.Printf("📈 Backoff increased to %d seconds (multiplier: %.1f)", int(state.currentInterval.Seconds()), state.backoffMultiplier)
			}
		}
	}()
}

// uploadBatch lee archivos de la cola y los envía a la nube
func uploadBatch(cfg Config) bool {
	// Buscar archivos JSON en la cola
	pattern := filepath.Join(cfg.QueuePath, "*.json")
	files, err := filepath.Glob(pattern)
	if err != nil {
		log.Printf("❌ Error globbing queue files: %v", err)
		return false
	}

	if len(files) == 0 {
		return true // Nada que hacer, se considera éxito
	}

	log.Printf("📤 Found %d files in queue...", len(files))

	var events []telemetry.Telemetry
	var failedFiles []string

	// Leer y procesar cada archivo
	for _, filePath := range files {
		data, err := os.ReadFile(filePath)
		if err != nil {
			log.Printf("⚠️  Failed to read %s: %v", filepath.Base(filePath), err)
			failedFiles = append(failedFiles, filePath)
			continue
		}

		var t telemetry.Telemetry
		if err := json.Unmarshal(data, &t); err != nil {
			log.Printf("⚠️  Failed to unmarshal %s: %v", filepath.Base(filePath), err)
			failedFiles = append(failedFiles, filePath)
			continue
		}

		events = append(events, t)
	}

	if len(events) == 0 {
		return false // Hubo archivos pero ninguno válido
	}

	payload := map[string]interface{}{
		"events": events,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("❌ Failed to marshal payload: %v", err)
		return false
	}

	// --- CAMBIO 2: Construcción correcta de la URL ---
	// Si config es "http://localhost:8000", esto genera:
	// "http://localhost:8000/api/agent/telemetry"
	baseURL := strings.TrimRight(cfg.CloudURL, "/")
	endpoint := fmt.Sprintf("%s/api/agent/telemetry", baseURL)
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(jsonData))
	if err != nil {
		log.Printf("❌ Failed to create request: %v", err)
		return false
	}

	// --- CAMBIO 3: Headers de Autenticación ---
	req.Header.Set("X-Agent-Key", cfg.AgentKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Go-SNMP-Agent/1.0")

	req.Header.Set("Accept", "application/json") // <--- IMPORTANTE

	log.Printf("📡 Uploading %d events to %s", len(events), endpoint)

	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("⚠️  Upload network error: %v", err)
		return false
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	// Validar respuesta (200 OK o 201 Created)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Printf("✅ Upload successful (status: %d)", resp.StatusCode)

		// Borrar archivos procesados
		for _, filePath := range files {
			// No borrar los que fallaron al leerse localmente
			isFailed := false
			for _, f := range failedFiles {
				if f == filePath {
					isFailed = true
					break
				}
			}
			if isFailed {
				continue
			}

			if err := os.Remove(filePath); err != nil {
				log.Printf("⚠️  Failed to delete %s: %v", filepath.Base(filePath), err)
			}
		}
		return true
	} else {
		// Error del servidor
		log.Printf("⚠️  Upload server error (status: %d): %s", resp.StatusCode, string(respBody))
		return false
	}
}
