// Package webfallback implementa scraping HTTP como contingencia cuando SNMP
// no reporta contadores de copia/escaneo en modelos Legacy de distintas marcas.
//
// Para agregar soporte a una nueva marca:
//  1. Crear un archivo <marca>.go en este mismo paquete.
//  2. Implementar la interfaz Fetcher.
//  3. Registrar la implementación con Register() en un bloque init().
//
// El caller (pkg/collector) solo usa Get(brand, ip) — no necesita conocer
// los detalles de ninguna marca en particular.
package webfallback

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Tipos públicos
// ---------------------------------------------------------------------------

// Counters contiene los contadores extraídos del servidor web embebido.
// Los campos opcionales (DADFScans, PlatenScans) se rellenan solo cuando el
// HTML del dispositivo los expone.
type Counters struct {
	TotalPages  int64
	CopyPages   int64
	PrintPages  int64
	ScanPages   int64 // DADFScans + PlatenScans cuando ambos están disponibles
	DADFScans   int64
	PlatenScans int64

	// Desglose Duplex / Simplex — disponible solo en firmware SyncThru 6.x+
	// (claves GXI_BILLING_DUPLEX/SIMPLEX_*). Permanecen en 0 cuando el
	// modelo no los expone; el caller nunca debe asumir que están rellenos.
	DuplexMono   int64 // GXI_BILLING_DUPLEX_BW_TOTAL_CNT
	DuplexColor  int64 // GXI_BILLING_DUPLEX_COLOR_TOTAL_CNT
	SimplexMono  int64 // GXI_BILLING_SIMPLEX_BW_TOTAL_CNT
	SimplexColor int64 // GXI_BILLING_SIMPLEX_COLOR_TOTAL_CNT
}

// Fetcher es la interfaz que debe implementar cada marca.
type Fetcher interface {
	// Fetch obtiene los contadores desde el servidor web embebido de la impresora.
	Fetch(ip string) (*Counters, error)
}

// ---------------------------------------------------------------------------
// Registro de implementaciones
// ---------------------------------------------------------------------------

var registry = map[string]Fetcher{}

// Register asocia una marca (en minúsculas) con su implementación.
// Se llama desde los bloques init() de cada archivo de marca.
func Register(brand string, f Fetcher) {
	registry[strings.ToLower(brand)] = f
}

// Get despacha la petición a la implementación registrada para la marca dada.
// Devuelve ErrNotSupported si no hay implementación para esa marca.
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

// ---------------------------------------------------------------------------
// Cliente HTTP compartido por todas las implementaciones
// ---------------------------------------------------------------------------

// SharedClient es el cliente HTTP con timeout estricto de 5 s reutilizado por
// todos los Fetchers. Las implementaciones de cada marca deben usarlo en lugar
// de crear clientes propios, salvo que necesiten configuración especial.
var SharedClient = &http.Client{
	Timeout: 5 * time.Second,
}
