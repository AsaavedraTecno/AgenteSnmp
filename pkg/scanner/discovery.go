package scanner

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/asaavedra/agent-snmp/pkg/snmp"
)

// DiscoveryResult contiene información de un dispositivo descubierto
type DiscoveryResult struct {
	IP              string
	Community       string
	SNMPVersion     string
	SysDescr        string
	SysObjectID     string
	IsResponsive    bool
	ResponseTime    time.Duration
	DiscoveredAt    time.Time
	Brand           string
	BrandConfidence float64
	Errors          []string
}

// DiscoveryConfig contiene configuración para el discovery
type DiscoveryConfig struct {
	MaxConcurrentConnections int
	TimeoutPerDevice         time.Duration
	Retries                  int
	Community                string
	SNMPVersion              string
	SNMPPort                 uint16
}

// DiscoveryScanner ejecuta escaneo SNMP en paralelo
type DiscoveryScanner struct {
	config DiscoveryConfig
}

// NewDiscoveryScanner crea un nuevo scanner de discovery
func NewDiscoveryScanner(config DiscoveryConfig) *DiscoveryScanner {
	return &DiscoveryScanner{config: config}
}

// Scan ejecuta el escaneo de IPs
func (ds *DiscoveryScanner) Scan(ctx context.Context, ips []string) ([]DiscoveryResult, error) {
	results := make([]DiscoveryResult, 0, len(ips))
	resultsChan := make(chan DiscoveryResult, len(ips))
	var wg sync.WaitGroup

	semaphore := make(chan struct{}, ds.config.MaxConcurrentConnections)

	fmt.Printf("Iniciando descubrimiento de %d IPs...\n", len(ips))
	startTime := time.Now()

	for _, ip := range ips {
		// Verificar si el contexto fue cancelado antes de lanzar la goroutine
		select {
		case <-ctx.Done():
			fmt.Println("Escaneo cancelado por contexto.")
			goto WaitAndReturn
		default:
		}

		wg.Add(1)

		go func(targetIP string) {
			defer wg.Done()

			// Adquirir slot
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done(): // Respetar cancelación mientras espera un slot
				return
			}
			defer func() { <-semaphore }()

			result := ds.probeIP(ctx, targetIP)

			// Enviar resultado
			select {
			case resultsChan <- result:
			case <-ctx.Done():
			}

		}(ip)
	}

WaitAndReturn:
	// Esperar a que todos terminen
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// Recolectar resultados
	for result := range resultsChan {
		if result.IsResponsive {
			results = append(results, result)
		}
	}

	fmt.Printf("Descubrimiento completado en %.2f segundos. Encontradas %d impresoras.\n",
		time.Since(startTime).Seconds(), len(results))

	return results, nil
}

// probeIP prueba un IP individual.
// Siempre intenta SNMP v2c primero; si falla por cualquier motivo (timeout,
// versión no soportada, comunidad incorrecta), hace un fallback rápido a v1.
// El campo SNMPVersion del resultado indica la versión que realmente respondió
// y debe propagarse al colector para que use la misma versión en la recolección.
func (ds *DiscoveryScanner) probeIP(ctx context.Context, ip string) DiscoveryResult {
	result := DiscoveryResult{
		IP:           ip,
		Community:    ds.config.Community,
		SNMPVersion:  "2c", // default; se actualiza si v1 es la que responde
		DiscoveredAt: time.Now(),
		IsResponsive: false,
	}

	startTime := time.Now()

	// Intentar v2c siempre como primera opción
	clientV2c := snmp.NewSNMPClient(
		ip, ds.config.SNMPPort, ds.config.Community, "2c",
		ds.config.TimeoutPerDevice, ds.config.Retries,
	)
	sysDescr, sysObjectID, err := clientV2c.PingDiscovery()

	if err != nil {
		// Fallback rápido a SNMP v1
		clientV1 := snmp.NewSNMPClient(
			ip, ds.config.SNMPPort, ds.config.Community, "1",
			ds.config.TimeoutPerDevice, ds.config.Retries,
		)
		sysDescr, sysObjectID, err = clientV1.PingDiscovery()
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("probe_failed_v2c_v1: %v", err))
			return result
		}
		result.SNMPVersion = "1"
		fmt.Printf("[%s] v2c sin respuesta → respondió en SNMP v1\n", ip)
	}

	result.SysDescr = sysDescr
	result.SysObjectID = sysObjectID
	result.IsResponsive = true
	result.ResponseTime = time.Since(startTime)

	result.Brand = detectBrand(result.SysDescr, result.SysObjectID)
	if result.Brand != "Generic" {
		result.BrandConfidence = 1.0
	} else {
		result.BrandConfidence = 0.5
	}

	return result
}

// detectBrand analiza el sysDescr para encontrar la marca
func detectBrand(sysDescr, sysObjectID string) string {
	lowerDesc := strings.ToLower(sysDescr)

	// Samsung suele aparecer como "Samsung", "Samsung Electronics", etc.
	if strings.Contains(lowerDesc, "samsung") {
		return "Samsung"
	}

	// HP
	if strings.Contains(lowerDesc, "hp") ||
		strings.Contains(lowerDesc, "hewlett") ||
		strings.Contains(lowerDesc, "jetdirect") {
		return "HP"
	}

	// Xerox
	if strings.Contains(lowerDesc, "xerox") ||
		strings.Contains(lowerDesc, "phaser") ||
		strings.Contains(lowerDesc, "workcentre") ||
		strings.Contains(lowerDesc, "versalink") ||
		strings.Contains(lowerDesc, "altalink") {
		return "Xerox"
	}

	// Kyocera
	if strings.Contains(lowerDesc, "kyocera") ||
		strings.Contains(lowerDesc, "ecosys") ||
		strings.Contains(lowerDesc, "taskalfa") {
		return "Kyocera"
	}

	// Epson
	if strings.Contains(lowerDesc, "epson") {
		return "Epson"
	}

	// Ricoh
	if strings.Contains(lowerDesc, "ricoh") ||
		strings.Contains(lowerDesc, "aficio") ||
		strings.Contains(lowerDesc, "mp c") {
		return "Ricoh"
	}

	// Lexmark
	if strings.Contains(lowerDesc, "lexmark") {
		return "Lexmark"
	}

	// Canon
	if strings.Contains(lowerDesc, "canon") || strings.Contains(lowerDesc, "imageclass") {
		return "Canon"
	}

	// Si no encontramos nada conocido
	return "Generic"
}
