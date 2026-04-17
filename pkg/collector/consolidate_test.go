package collector

import (
	"testing"

	"github.com/asaavedra/agent-snmp/pkg/models"
)

// makeData construye un PrinterData mínimo para testear consolidateCounters.
func makeData(total, mono, color int64, colorSupply bool) *PrinterData {
	d := &PrinterData{
		IP:                 "192.168.1.1",
		NormalizedCounters: map[string]interface{}{},
	}
	if total > 0 {
		d.NormalizedCounters["total_pages"] = total
	}
	if mono > 0 {
		d.NormalizedCounters["mono_pages"] = mono
	}
	if color > 0 {
		d.NormalizedCounters["color_pages"] = color
	}
	if colorSupply {
		d.Supplies = []models.SupplyData{
			{Color: "cyan"},
		}
	}
	return d
}

func consolidate(data *PrinterData, profiled bool) {
	dc := &DataCollector{}
	if profiled {
		data.CounterConfidence = "profiled"
	} else {
		data.CounterConfidence = "standard"
	}
	dc.consolidateCounters(data)
}

func getCounter(data *PrinterData, key string) int64 {
	return getInt(data.NormalizedCounters, key)
}

// ─────────────────────────────────────────────────────────────────────────────
// Modo PROFILED — color
// ─────────────────────────────────────────────────────────────────────────────

func TestConsolidate_Profiled_Color_AllPresent(t *testing.T) {
	// Total, mono y color vienen todos del perfil → sin síntesis.
	d := makeData(1000, 800, 200, true)
	consolidate(d, true)
	if getCounter(d, "total_pages") != 1000 {
		t.Errorf("total: got %d want 1000", getCounter(d, "total_pages"))
	}
	if getCounter(d, "mono_pages") != 800 {
		t.Errorf("mono: got %d want 800", getCounter(d, "mono_pages"))
	}
	if getCounter(d, "color_pages") != 200 {
		t.Errorf("color: got %d want 200", getCounter(d, "color_pages"))
	}
}

func TestConsolidate_Profiled_Color_CasoA_TotalDesdeMono(t *testing.T) {
	// Caso A: mono + color pero total = 0 → sintetizar total.
	d := makeData(0, 800, 200, true)
	consolidate(d, true)
	if getCounter(d, "total_pages") != 1000 {
		t.Errorf("total: got %d want 1000", getCounter(d, "total_pages"))
	}
}

func TestConsolidate_Profiled_Color_CasoB_MonoDesdeTotalColor(t *testing.T) {
	// Caso B: total + color pero mono = 0 → sintetizar mono.
	d := makeData(1000, 0, 200, true)
	consolidate(d, true)
	if getCounter(d, "mono_pages") != 800 {
		t.Errorf("mono: got %d want 800", getCounter(d, "mono_pages"))
	}
}

func TestConsolidate_Profiled_Color_CasoC_ColorDesdeTotalMono(t *testing.T) {
	// Caso C: total + mono pero color = 0 → sintetizar color.
	d := makeData(1000, 800, 0, true)
	consolidate(d, true)
	if getCounter(d, "color_pages") != 200 {
		t.Errorf("color: got %d want 200", getCounter(d, "color_pages"))
	}
}

func TestConsolidate_Profiled_Color_CasoD_SoloTotal(t *testing.T) {
	// Caso D: solo total, sin mono ni color → mono = total como fallback.
	d := makeData(1000, 0, 0, true)
	consolidate(d, true)
	if getCounter(d, "mono_pages") != 1000 {
		t.Errorf("mono fallback: got %d want 1000", getCounter(d, "mono_pages"))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Modo PROFILED — monocromática
// ─────────────────────────────────────────────────────────────────────────────

func TestConsolidate_Profiled_Mono_ColorForzadoCero(t *testing.T) {
	// Sin suministros de color y sin color_pages en el perfil (=0)
	// → isColorDevice=false → color queda en 0.
	// Nota: si el perfil entregara color_pages>0, el colector marcaría
	// isColorDevice=true — ese caso se cubre en CasoC.
	d := makeData(500, 500, 0, false)
	consolidate(d, true)
	if getCounter(d, "color_pages") != 0 {
		t.Errorf("color debe ser 0 en mono: got %d", getCounter(d, "color_pages"))
	}
	if getCounter(d, "mono_pages") != 500 {
		t.Errorf("mono: got %d want 500", getCounter(d, "mono_pages"))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Modo STANDARD (RFC 3805)
// ─────────────────────────────────────────────────────────────────────────────

func TestConsolidate_Standard_SoloTotal(t *testing.T) {
	d := makeData(300, 0, 0, false)
	consolidate(d, false)
	if getCounter(d, "total_pages") != 300 {
		t.Errorf("total: got %d want 300", getCounter(d, "total_pages"))
	}
	if getCounter(d, "mono_pages") != 300 {
		t.Errorf("mono: got %d want 300", getCounter(d, "mono_pages"))
	}
	if getCounter(d, "color_pages") != 0 {
		t.Errorf("color: got %d want 0", getCounter(d, "color_pages"))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// isSuspiciousValue
// ─────────────────────────────────────────────────────────────────────────────

func TestIsSuspiciousValue(t *testing.T) {
	cases := []struct {
		val  int64
		want bool
	}{
		{0, false},
		{238962, false},
		{2_000_000_000, false},    // antes era rechazado, ahora no
		{9_999_999_999, false},    // justo bajo el umbral
		{10_000_000_001, true},    // sobre el umbral
		{99_999_999_999, true},
	}
	for _, c := range cases {
		got := isSuspiciousValue(c.val)
		if got != c.want {
			t.Errorf("isSuspiciousValue(%d) = %v, want %v", c.val, got, c.want)
		}
	}
}
