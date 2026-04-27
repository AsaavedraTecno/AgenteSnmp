package collector

// web_fallback.go — Adaptador entre DataCollector y pkg/webfallback.
//
// Intencionalmente delgado: solo decide cuándo activar el fallback y
// cómo volcar la jerarquía Counters en el mapa NormalizedCounters plano
// que usa el resto del colector.

import (
	"log"
	"strings"

	"github.com/asaavedra/agent-snmp/pkg/webfallback"
)

// applyWebCountersFallback invoca el fetcher web de la marca correspondiente
// cuando SNMP retornó copy_pages == 0 y scan_pages == 0.
//
// Solo sobreescribe campos que llegaron como 0 desde SNMP — nunca pisa
// datos SNMP válidos. Permite mezcla coherente SNMP + Web.
func (dc *DataCollector) applyWebCountersFallback(data *PrinterData) {
	if !webfallback.Supported(data.Brand) {
		log.Printf("[%s][WEB_FALLBACK] Marca %q sin soporte web, omitiendo\n", data.IP, data.Brand)
		return
	}

	copyVal := getInt(data.NormalizedCounters, "copy_pages")
	scanVal := getInt(data.NormalizedCounters, "scan_pages")

	log.Printf("[%s][WEB_FALLBACK] copy_pages=%d scan_pages=%d via SNMP → intentando HTTP (%s)\n",
		data.IP, copyVal, scanVal, data.Brand)

	wc, err := webfallback.Get(data.Brand, data.IP)
	if err != nil {
		log.Printf("[%s][WEB_FALLBACK] ⚠️  %v\n", data.IP, err)
	}
	if wc == nil {
		log.Printf("[%s][WEB_FALLBACK] ❌ Sin respuesta del servidor web\n", data.IP)
		return
	}

	log.Printf("[%s][WEB_FALLBACK] ✅ total=%d copy=%d print=%d scan=%d duplex=%d simplex=%d\n",
		data.IP,
		wc.Absolute.Total,
		wc.LogicalMatrix.ByFunction.Copy,
		wc.LogicalMatrix.ByFunction.Print,
		wc.HardwareUsage.TotalScans,
		wc.LogicalMatrix.ByMode.Duplex,
		wc.LogicalMatrix.ByMode.Simplex,
	)

	// ── Contadores principales ───────────────────────────────────────────────
	if getInt(data.NormalizedCounters, "total_pages") == 0 && wc.Absolute.Total > 0 {
		data.NormalizedCounters["total_pages"] = wc.Absolute.Total
	}
	if getInt(data.NormalizedCounters, "mono_pages") == 0 && wc.Absolute.Mono > 0 {
		data.NormalizedCounters["mono_pages"] = wc.Absolute.Mono
	}
	if getInt(data.NormalizedCounters, "color_pages") == 0 && wc.Absolute.Color > 0 {
		data.NormalizedCounters["color_pages"] = wc.Absolute.Color
	}
	if copyVal == 0 && wc.LogicalMatrix.ByFunction.Copy > 0 {
		data.NormalizedCounters["copy_pages"] = wc.LogicalMatrix.ByFunction.Copy
	}
	if getInt(data.NormalizedCounters, "print_pages") == 0 && wc.LogicalMatrix.ByFunction.Print > 0 {
		data.NormalizedCounters["print_pages"] = wc.LogicalMatrix.ByFunction.Print
	}
	if scanVal == 0 && wc.HardwareUsage.TotalScans > 0 {
		data.NormalizedCounters["scan_pages"] = wc.HardwareUsage.TotalScans
	}

	// ── Desglose por función (fax, informes) — opcional ─────────────────────
	if wc.LogicalMatrix.ByFunction.FaxPrint > 0 {
		data.NormalizedCounters["fax_pages"] = wc.LogicalMatrix.ByFunction.FaxPrint
	}

	// ── Desglose por modo (solo SyncThru 6.x+) ──────────────────────────────
	if wc.LogicalMatrix.ByMode.Duplex > 0 {
		data.NormalizedCounters["duplex_pages"] = wc.LogicalMatrix.ByMode.Duplex
	}
	if wc.LogicalMatrix.ByMode.Simplex > 0 {
		data.NormalizedCounters["simplex_pages"] = wc.LogicalMatrix.ByMode.Simplex
	}

	// ── Desglose por destino de escaneo ─────────────────────────────────────
	dst := wc.LogicalMatrix.ByDestination
	if dst.Email > 0 {
		data.NormalizedCounters["scan_email"] = dst.Email
	}
	if dst.FTP > 0 {
		data.NormalizedCounters["scan_ftp"] = dst.FTP
	}
	if dst.SMB > 0 {
		data.NormalizedCounters["scan_smb"] = dst.SMB
	}
	if dst.USB > 0 {
		data.NormalizedCounters["scan_usb"] = dst.USB
	}
	if dst.Others > 0 {
		data.NormalizedCounters["scan_others"] = dst.Others
	}

	data.CounterConfidence = "profiled+web"
}

// webFallbackNeeded informa si el dispositivo es una marca con soporte web
// y sus contadores de copia/escaneo llegaron a cero desde SNMP.
// Se llama desde collectCountersFromYAML.
// webFallbackNeeded activa el scraping HTTP cuando la marca tiene soporte web
// y al menos uno de los contadores estratégicos (copy o scan) llegó a cero
// desde SNMP — indicando que el perfil no los expone vía MIB privada.
// Se usa OR para capturar el caso frecuente donde una impresora tiene copias
// válidas pero scan_pages=0 (o viceversa).
func webFallbackNeeded(data *PrinterData) bool {
	brand := strings.ToLower(data.Brand)
	supported := brand == "samsung" || brand == "xerox"
	return supported && (getInt(data.NormalizedCounters, "copy_pages") == 0 ||
		getInt(data.NormalizedCounters, "scan_pages") == 0)
}
