package collector

import "testing"

func TestWebFallbackNeeded(t *testing.T) {
	cases := []struct {
		name      string
		brand     string
		copyPages int64
		scanPages int64
		want      bool
	}{
		// Marcas sin soporte → nunca activa
		{"hp sin soporte", "hp", 0, 0, false},
		{"kyocera sin soporte", "kyocera", 0, 0, false},
		{"desconocida sin soporte", "", 0, 0, false},

		// Samsung: activa cuando falta copy O scan
		{"samsung copy=0 scan=0", "samsung", 0, 0, true},
		{"samsung copy=0 scan>0", "samsung", 0, 100, true},  // falta copy
		{"samsung copy>0 scan=0", "samsung", 50, 0, true},   // falta scan
		{"samsung ambos>0", "samsung", 50, 100, false},      // todo presente, no activa

		// Xerox: misma lógica que samsung
		{"xerox copy=0 scan=0", "xerox", 0, 0, true},
		{"xerox copy=0 scan>0", "xerox", 0, 500, true},
		{"xerox copy>0 scan=0", "xerox", 200, 0, true},
		{"xerox ambos>0", "xerox", 200, 500, false},

		// Case-insensitive
		{"Samsung mayúscula", "Samsung", 0, 0, true},
		{"XEROX mayúscula", "XEROX", 0, 0, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := &PrinterData{
				Brand:              c.brand,
				NormalizedCounters: map[string]interface{}{},
			}
			if c.copyPages > 0 {
				d.NormalizedCounters["copy_pages"] = c.copyPages
			}
			if c.scanPages > 0 {
				d.NormalizedCounters["scan_pages"] = c.scanPages
			}
			got := webFallbackNeeded(d)
			if got != c.want {
				t.Errorf("webFallbackNeeded() = %v, want %v", got, c.want)
			}
		})
	}
}
