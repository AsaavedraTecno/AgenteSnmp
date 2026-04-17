// Package webfallback implementa scraping HTTP como contingencia cuando SNMP
// no reporta contadores de copia/escaneo en modelos Legacy de distintas marcas.
//
// Para agregar soporte a una nueva marca:
//  1. Crear un archivo <marca>.go en este mismo paquete.
//  2. Implementar la interfaz Fetcher.
//  3. Registrar la implementación con Register() en un bloque init().
package webfallback

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Sub-structs jerárquicos
// ─────────────────────────────────────────────────────────────────────────────

// CountersAbsolute contiene los totales brutos de impresión.
// "mono" y "color" se rellenan cuando el firmware los expone; de lo contrario
// permanecen en 0 y el caller puede inferirlos si es necesario.
type CountersAbsolute struct {
	Total int64 `json:"total"`
	Mono  int64 `json:"mono"`
	Color int64 `json:"color"`
}

// CountersByFunction desglosa impresiones por función lógica.
// Columnas de la fila "Impresiones totales" del HTML SyncThru.
type CountersByFunction struct {
	Print    int64 `json:"print"`
	Copy     int64 `json:"copy"`
	FaxPrint int64 `json:"fax_print"`
	Reports  int64 `json:"reports"`
}

// CountersByMode desglosa impresiones por modo físico de alimentación.
// "simplex" y "duplex" son totales (mono + color combinados).
type CountersByMode struct {
	Simplex int64 `json:"simplex"`
	Duplex  int64 `json:"duplex"`
}

// CountersByDestination desglosa escaneos/envíos por destino.
// Columnas de la tabla "Uso envío" del HTML SyncThru.
type CountersByDestination struct {
	Email     int64 `json:"email"`
	FTP       int64 `json:"ftp"`
	SMB       int64 `json:"smb"`
	USB       int64 `json:"usb"`
	Others    int64 `json:"others"`    // "PC" o "Enviar a otros"
	TotalSend int64 `json:"total_send"` // suma de todas las columnas anteriores
}

// CountersLogicalMatrix agrupa los tres ejes lógicos de análisis.
type CountersLogicalMatrix struct {
	ByFunction    CountersByFunction    `json:"by_function"`
	ByMode        CountersByMode        `json:"by_mode"`
	ByDestination CountersByDestination `json:"by_destination"`
}

// CountersHardwareUsage contiene métricas de desgaste físico del motor.
type CountersHardwareUsage struct {
	TotalScans   int64 `json:"total_scans"`   // = ByDestination.TotalSend
	EngineCycles int64 `json:"engine_cycles"` // disponible solo vía SNMP
}

// Counters es la estructura raíz que devuelve todo Fetcher.
// Serializada con json.Marshal produce exactamente la jerarquía esperada
// por la API de Laravel y los pipelines de ML.
//
// Ejemplo de salida JSON:
//
//	{
//	  "absolute":       { "total": 238962, "mono": 0, "color": 0 },
//	  "logical_matrix": {
//	    "by_function":    { "print": 215232, "copy": 23513, ... },
//	    "by_mode":        { "simplex": 305,  "duplex": 1510 },
//	    "by_destination": { "email": 25941,  "total_send": 60360, ... }
//	  },
//	  "hardware_usage": { "total_scans": 60360, "engine_cycles": 0 },
//	  "confidence":     "profiled+web"
//	}
type Counters struct {
	Absolute      CountersAbsolute      `json:"absolute"`
	LogicalMatrix CountersLogicalMatrix `json:"logical_matrix"`
	HardwareUsage CountersHardwareUsage `json:"hardware_usage"`
	Confidence    string                `json:"confidence,omitempty"`
}

// MergeCounters fusiona dos estructuras Counters, dando prioridad a los datos web.
// Itera/evalúa los campos matriciales.
// Regla de prioridad: Si web.Campo > 0, usa web.Campo.
// Regla de rescate: Si web.Campo == 0 y snmp.Campo > 0, MANTÉN snmp.Campo. NO lo sobreescribas con 0.
func MergeCounters(snmp *Counters, web *Counters) *Counters {
	if snmp == nil && web == nil {
		return &Counters{}
	}
	if snmp == nil {
		return web
	}
	if web == nil {
		return snmp
	}

	merged := &Counters{
		Confidence: snmp.Confidence,
	}

	mergeInt := func(s, w int64) int64 {
		if w > 0 {
			return w
		}
		return s
	}

	// Absolutos
	merged.Absolute.Total = mergeInt(snmp.Absolute.Total, web.Absolute.Total)
	merged.Absolute.Mono = mergeInt(snmp.Absolute.Mono, web.Absolute.Mono)
	merged.Absolute.Color = mergeInt(snmp.Absolute.Color, web.Absolute.Color)

	// Por Función
	merged.LogicalMatrix.ByFunction.Print = mergeInt(snmp.LogicalMatrix.ByFunction.Print, web.LogicalMatrix.ByFunction.Print)
	merged.LogicalMatrix.ByFunction.Copy = mergeInt(snmp.LogicalMatrix.ByFunction.Copy, web.LogicalMatrix.ByFunction.Copy)
	merged.LogicalMatrix.ByFunction.FaxPrint = mergeInt(snmp.LogicalMatrix.ByFunction.FaxPrint, web.LogicalMatrix.ByFunction.FaxPrint)
	merged.LogicalMatrix.ByFunction.Reports = mergeInt(snmp.LogicalMatrix.ByFunction.Reports, web.LogicalMatrix.ByFunction.Reports)

	// Por Modo
	merged.LogicalMatrix.ByMode.Simplex = mergeInt(snmp.LogicalMatrix.ByMode.Simplex, web.LogicalMatrix.ByMode.Simplex)
	merged.LogicalMatrix.ByMode.Duplex = mergeInt(snmp.LogicalMatrix.ByMode.Duplex, web.LogicalMatrix.ByMode.Duplex)

	// Por Destino
	merged.LogicalMatrix.ByDestination.Email = mergeInt(snmp.LogicalMatrix.ByDestination.Email, web.LogicalMatrix.ByDestination.Email)
	merged.LogicalMatrix.ByDestination.FTP = mergeInt(snmp.LogicalMatrix.ByDestination.FTP, web.LogicalMatrix.ByDestination.FTP)
	merged.LogicalMatrix.ByDestination.SMB = mergeInt(snmp.LogicalMatrix.ByDestination.SMB, web.LogicalMatrix.ByDestination.SMB)
	merged.LogicalMatrix.ByDestination.USB = mergeInt(snmp.LogicalMatrix.ByDestination.USB, web.LogicalMatrix.ByDestination.USB)
	merged.LogicalMatrix.ByDestination.Others = mergeInt(snmp.LogicalMatrix.ByDestination.Others, web.LogicalMatrix.ByDestination.Others)
	merged.LogicalMatrix.ByDestination.TotalSend = mergeInt(snmp.LogicalMatrix.ByDestination.TotalSend, web.LogicalMatrix.ByDestination.TotalSend)

	// Uso de Hardware
	merged.HardwareUsage.TotalScans = mergeInt(snmp.HardwareUsage.TotalScans, web.HardwareUsage.TotalScans)
	merged.HardwareUsage.EngineCycles = mergeInt(snmp.HardwareUsage.EngineCycles, web.HardwareUsage.EngineCycles)

	return merged
}

// ─────────────────────────────────────────────────────────────────────────────
// Interfaz y registro
// ─────────────────────────────────────────────────────────────────────────────

// Fetcher es la interfaz que debe implementar cada marca.
type Fetcher interface {
	Fetch(ip string) (*Counters, error)
}

var registry = map[string]Fetcher{}

// Register asocia una marca (en minúsculas) con su implementación.
// Se llama desde los bloques init() de cada archivo de marca.
func Register(brand string, f Fetcher) {
	registry[strings.ToLower(brand)] = f
}

// Get despacha la petición a la implementación registrada para la marca dada.
func Get(brand, ip string) (*Counters, error) {
	f, ok := registry[strings.ToLower(brand)]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotSupported, brand)
	}
	return f.Fetch(ip)
}

// Supported informa si existe una implementación registrada para la marca.
func Supported(brand string) bool {
	_, ok := registry[strings.ToLower(brand)]
	return ok
}

// ErrNotSupported se devuelve cuando la marca no tiene implementación web.
var ErrNotSupported = fmt.Errorf("webfallback: marca sin soporte")

// ─────────────────────────────────────────────────────────────────────────────
// Cliente HTTP compartido
// ─────────────────────────────────────────────────────────────────────────────

// SharedClient es el cliente HTTP con timeout estricto de 5 s reutilizado por
// todos los Fetchers.
var SharedClient = &http.Client{
	Timeout: 5 * time.Second,
}

// validateIP comprueba que ip sea una IP válida (v4 o v6), con o sin puerto.
// Los Fetchers deben llamarlo antes de construir URLs para evitar
// redirecciones maliciosas por IPs con caracteres especiales (SSRF).
func validateIP(ip string) error {
	host := ip
	// Si viene en formato host:port, extraemos solo el host.
	if h, _, err := net.SplitHostPort(ip); err == nil {
		host = h
	}
	if net.ParseIP(host) == nil {
		return fmt.Errorf("webfallback: IP inválida: %q", ip)
	}
	return nil
}
