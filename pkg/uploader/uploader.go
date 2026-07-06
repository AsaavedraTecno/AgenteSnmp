package uploader

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
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
	Enabled        bool   `yaml:"enabled"`
	CloudURL       string `yaml:"cloud_url"`
	AgentKey       string `yaml:"agent_key"`
	IntervalSecs   int    `yaml:"interval_secs"`
	MaxBackoffSecs int    `yaml:"max_backoff_secs"`
	QueuePath      string `yaml:"sinks_file_path"`
	SkipTLSVerify  bool   `yaml:"skip_tls_verify"`
	BatchSize      int    `yaml:"batch_size"`
}

// UploaderState mantiene el estado del uploader para backoff exponencial
type UploaderState struct {
	baseInterval      time.Duration
	currentInterval   time.Duration
	maxBackoff        time.Duration
	backoffMultiplier float64
}

var uploadTrigger = make(chan struct{}, 1)

// TriggerUpload solicita un intento de envío inmediato
func TriggerUpload() {
	select {
	case uploadTrigger <- struct{}{}:
	default:
		// Ya hay un trigger pendiente
	}
}

// StartUploader inicia una goroutine que envía eventos periódicamente a la nube.
// Recibe un contexto y una función getConfig para obtener siempre la configuración más reciente.
func StartUploader(ctx context.Context, getConfig func() Config) {
	initialCfg := getConfig()
	if !initialCfg.Enabled {
		log.Println("⚠️  Uploader disabled in initial config")
	}

	maxBackoff := time.Duration(initialCfg.MaxBackoffSecs) * time.Second
	if maxBackoff == 0 {
		maxBackoff = 5 * time.Minute // Default 5 minutos
	}

	go func() {
		state := &UploaderState{
			baseInterval:      time.Duration(initialCfg.IntervalSecs) * time.Second,
			currentInterval:   time.Duration(initialCfg.IntervalSecs) * time.Second,
			maxBackoff:        maxBackoff,
			backoffMultiplier: 1.0,
		}

		// Si el intervalo es 0 (no configurado aún), usar 30s por defecto para el ticker
		if state.baseInterval <= 0 {
			state.baseInterval = 30 * time.Second
			state.currentInterval = 30 * time.Second
		}

		ticker := time.NewTicker(state.currentInterval)
		defer ticker.Stop()

		log.Printf("🚀 Uploader started (base interval: %s, host: %s)",
			state.baseInterval, initialCfg.CloudURL)

		for {
			select {
			case <-ticker.C:
				// Intervalo regular
			case <-uploadTrigger:
				log.Println("⚡ Envío disparado manualmente tras escaneo...")
			case <-ctx.Done():
				log.Println("uploader: shutting down gracefully")
				return
			}

			// Obtener configuración fresca (por si cambió SkipTLSVerify o AgentKey)
			currentCfg := getConfig()
			if !currentCfg.Enabled {
				time.Sleep(10 * time.Second)
				continue
			}

			// [P2] Limpieza de archivos antiguos (TTL 3 días) para evitar llenar el disco offline
			purgeOldFiles(currentCfg.QueuePath, 72*time.Hour)

			success := Upload(ctx, currentCfg)

			// Ajustar intervalo (Backoff Exponencial)
			if success {
				if state.backoffMultiplier != 1.0 {
					log.Printf("✅ Backoff reset to base interval (%d seconds)", currentCfg.IntervalSecs)
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

// Upload lee archivos de la cola y los envía a la nube en batches
func Upload(ctx context.Context, cfg Config) bool {
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

	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 50
	}

	log.Printf("📤 Encontrados %d archivos en cola para subir (batch size: %d)...", len(files), batchSize)

	allSuccess := true
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.SkipTLSVerify},
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   15 * time.Second,
	}

	baseURL := strings.TrimRight(cfg.CloudURL, "/")
	endpoint := fmt.Sprintf("%s/api/agent/telemetry", baseURL)
	
	const maxResponseSize = 1024 * 1024 // 1MB

	for i := 0; i < len(files); i += batchSize {
		end := i + batchSize
		if end > len(files) {
			end = len(files)
		}
		batchFiles := files[i:end]

		// Usar un buffer en lugar de pipe para poder calcular el HMAC del body completo
		var bodyBuf bytes.Buffer
		enc := json.NewEncoder(&bodyBuf)
		
		// Enviar como un BatchPayload que tiene "Readings"
		bodyBuf.WriteString(`{"readings":[`)
		first := true
		
		for _, filePath := range batchFiles {
			f, err := os.Open(filePath)
			if err != nil {
				log.Printf("⚠️  Error leyendo %s: %v", filepath.Base(filePath), err)
				continue
			}

			var tel telemetry.Telemetry
			if err := json.NewDecoder(f).Decode(&tel); err != nil {
				log.Printf("❌ Error parseando JSON %s: %v", filepath.Base(filePath), err)
				f.Close()
				continue
			}
			f.Close() // cerrar inmediatamente, no defer en loop

			// Inyectar CollectionMethod si es un archivo antiguo que no lo tenía
			if tel.Source.CollectionMethod == "" {
				tel.Source.CollectionMethod = "snmp"
			}

			if !first {
				bodyBuf.WriteString(`,`)
			}
			_ = enc.Encode(tel)
			first = false
		}
		bodyBuf.WriteString(`]}`)

		timestamp := fmt.Sprintf("%d", time.Now().Unix())

		h := sha256.New()
		h.Write([]byte(cfg.AgentKey))
		hashedKey := hex.EncodeToString(h.Sum(nil))

		mac := hmac.New(sha256.New, []byte(hashedKey))
		mac.Write(bodyBuf.Bytes())
		mac.Write([]byte(timestamp))
		signature := hex.EncodeToString(mac.Sum(nil))

		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, &bodyBuf)
		if err != nil {
			log.Printf("❌ Error creando request batch: %v", err)
			allSuccess = false
			continue
		}

		req.Header.Set("X-Agent-Key", cfg.AgentKey)
		req.Header.Set("X-Timestamp", timestamp)
		req.Header.Set("X-Signature", signature)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "Go-SNMP-Agent/1.0")

		log.Printf("📡 Subiendo batch de telemetrías stream (HMAC firmado)...")

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("⚠️  Error de red subiendo batch: %v", err)
			allSuccess = false
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			// Éxito: Borrar los archivos
			for _, fp := range batchFiles {
				os.Remove(fp)
			}
			log.Printf("✅ Batch subido con éxito (%d archivos borrados)", len(batchFiles))
		} else if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != 429 {
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
			log.Printf("⚠️  Batch failed with %d (Error: %s). Descartando %d archivos corruptos para evitar bloqueo.", resp.StatusCode, string(respBody), len(batchFiles))
			// Error permanente de cliente, descartar archivos para no atascar la cola
			for _, fp := range batchFiles {
				os.Remove(fp)
			}
			// No marcamos allSuccess = false para que no aumente el backoff por data corrupta.
		} else {
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
			log.Printf("⚠️  Batch failed: HTTP %d (Error: %s)", resp.StatusCode, string(respBody))
			allSuccess = false
		}
		resp.Body.Close()
	}

	return allSuccess
}

func purgeOldFiles(queuePath string, maxAge time.Duration) {
	if queuePath == "" {
		return
	}
	
	pattern := filepath.Join(queuePath, "*.json")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return
	}

	cutoff := time.Now().Add(-maxAge)
	deletedCount := 0

	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(file); err == nil {
				deletedCount++
			}
		}
	}

	if deletedCount > 0 {
		log.Printf("🧹 Limpieza offline: %d archivos antiguos (> %v) eliminados para liberar disco", deletedCount, maxAge)
	}
}
