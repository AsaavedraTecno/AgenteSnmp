package collector

// web_fallback.go — Adaptador entre DataCollector y pkg/webfallback.
//
// Este archivo es intencionalmente delgado: solo sabe cuándo activar el
// fallback y cómo volcar el resultado en NormalizedCounters. El scraping
// real vive en pkg/webfallback/<marca>.go.

import (
	"fmt"

	"github.com/asaavedra/agent-snmp/pkg/webfallback"
)

// applyWebCountersFallback invoca el fetcher web de la marca correspondiente
// cuando SNMP retornó copy_pages == 0 y scan_pages == 0.
//
// Solo sobreescribe los campos que llegaron como 0 — nunca pisa datos SNMP
// válidos. Esto permite mezcla coherente SNMP + Web.
func (dc *DataCollector) applyWebCountersFallback(data *PrinterData) {
	if !webfallback.Supported(data.Brand) {
		fmt.Printf("[%s][WEB_FALLBACK] Marca %q sin soporte web, omitiendo\n", data.IP, data.Brand)
		return
	}

	copyVal := getInt(data.NormalizedCounters, "copy_pages")
	scanVal := getInt(data.NormalizedCounters, "scan_pages")

	fmt.Printf("[%s][WEB_FALLBACK] copy_pages=%d scan_pages=%d via SNMP → intentando HTTP (%s)\n",
		data.IP, copyVal, scanVal, data.Brand)

	wc, err := webfallback.Get(data.Brand, data.IP)
	if err != nil {
		// Error parcial: puede haber extraído algunos campos igualmente.
		fmt.Printf("[%s][WEB_FALLBACK] ⚠️  %v\n", data.IP, err)
	}
	if wc == nil {
		fmt.Printf("[%s][WEB_FALLBACK] ❌ Sin respuesta del servidor web\n", data.IP)
		return
	}

	fmt.Printf("[%s][WEB_FALLBACK] ✅ total=%d copy=%d print=%d scan=%d (dadf=%d platen=%d) duplex_mono=%d duplex_color=%d\n",
		data.IP, wc.TotalPages, wc.CopyPages, wc.PrintPages, wc.ScanPages,
		wc.DADFScans, wc.PlatenScans, wc.DuplexMono, wc.DuplexColor)

	// Aplicar solo los campos que SNMP no proporcionó.
	if copyVal == 0 && wc.CopyPages > 0 {
		data.NormalizedCounters["copy_pages"] = wc.CopyPages
	}
	if scanVal == 0 && wc.ScanPages > 0 {
		data.NormalizedCounters["scan_pages"]   = wc.ScanPages
		data.NormalizedCounters["dadf_scans"]   = wc.DADFScans
		data.NormalizedCounters["platen_scans"] = wc.PlatenScans
	}
	if getInt(data.NormalizedCounters, "print_pages") == 0 && wc.PrintPages > 0 {
		data.NormalizedCounters["print_pages"] = wc.PrintPages
	}
	if getInt(data.NormalizedCounters, "total_pages") == 0 && wc.TotalPages > 0 {
		data.NormalizedCounters["total_pages"] = wc.TotalPages
	}

	// Desglose Duplex/Simplex — opcionales, solo Samsung SyncThru 6.x+.
	// Se escriben incondicionalmente (sobrescriben 0 o actualizan el dato anterior).
	if wc.DuplexMono > 0 {
		data.NormalizedCounters["duplex_mono_pages"] = wc.DuplexMono
	}
	if wc.DuplexColor > 0 {
		data.NormalizedCounters["duplex_color_pages"] = wc.DuplexColor
	}
	if wc.SimplexMono > 0 {
		data.NormalizedCounters["simplex_mono_pages"] = wc.SimplexMono
	}
	if wc.SimplexColor > 0 {
		data.NormalizedCounters["simplex_color_pages"] = wc.SimplexColor
	}

	data.CounterConfidence = "profiled+web"
}
