package webfallback

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Fixtures HTML
// ─────────────────────────────────────────────────────────────────────────────

// htmlXeroxES simula /counters/usage.php en firmware español (AltaLink C8055).
const htmlXeroxES = `
<html><body>
<table>
  <tr><td>Total de impresiones</td><td>238,962</td></tr>
  <tr><td>Impresiones en negro</td><td>215,232</td></tr>
  <tr><td>Impresiones en color</td><td>23,730</td></tr>
  <tr><td>Impresiones impresas en negro</td><td>200,000</td></tr>
  <tr><td>Impresiones impresas en color</td><td>15,232</td></tr>
  <tr><td>Impresiones copiadas en negro</td><td>15,232</td></tr>
  <tr><td>Impresiones copiadas en color</td><td>8,498</td></tr>
  <tr><td>Impresiones individuales</td><td>305</td></tr>
  <tr><td>Hojas copiadas a dos caras en negro</td><td>400</td></tr>
  <tr><td>Hojas copiadas a dos caras en color</td><td>300</td></tr>
  <tr><td>Hojas impresas a dos caras en negro</td><td>500</td></tr>
  <tr><td>Hojas impresas a dos caras en color</td><td>310</td></tr>
  <tr><td>Imágenes de e-mail enviadas</td><td>25,941</td></tr>
  <tr><td>Imágenes de escaneado en red enviadas</td><td>23,056</td></tr>
</table>
</body></html>
`

// htmlXeroxEN simula /counters/usage.php en firmware inglés (VersaLink C405).
const htmlXeroxEN = `
<html><body>
<table>
  <tr><td>Total Impressions</td><td>100000</td></tr>
  <tr><td>Black Impressions</td><td>80000</td></tr>
  <tr><td>Color Impressions</td><td>20000</td></tr>
  <tr><td>Black Print Impressions</td><td>50000</td></tr>
  <tr><td>Color Print Impressions</td><td>10000</td></tr>
  <tr><td>Black Copy Impressions</td><td>30000</td></tr>
  <tr><td>Color Copy Impressions</td><td>10000</td></tr>
  <tr><td>1 Sided Impressions</td><td>1500</td></tr>
  <tr><td>2 Sided Black Copy Sheets</td><td>200</td></tr>
  <tr><td>2 Sided Color Copy Sheets</td><td>100</td></tr>
  <tr><td>2 Sided Black Print Sheets</td><td>300</td></tr>
  <tr><td>2 Sided Color Print Sheets</td><td>150</td></tr>
  <tr><td>Email Images Sent</td><td>5000</td></tr>
  <tr><td>Network Scanning Images Sent</td><td>3000</td></tr>
</table>
</body></html>
`

// htmlXeroxNbsp simula firmware que usa &nbsp; como relleno entre etiquetas.
const htmlXeroxNbsp = `
<html><body>
<table>
  <tr><td>&nbsp;&nbsp;Total de impresiones&nbsp;</td><td>&nbsp;500&nbsp;</td></tr>
</table>
</body></html>
`

// ─────────────────────────────────────────────────────────────────────────────
// Tests de extractXeroxVal
// ─────────────────────────────────────────────────────────────────────────────

func TestExtractXeroxVal_ES(t *testing.T) {
	cases := []struct {
		keywords []string
		want     int64
	}{
		{[]string{"Total de impresiones"}, 238962},
		{[]string{"Impresiones en negro"}, 215232},
		{[]string{"Impresiones en color"}, 23730},
		{[]string{"Impresiones individuales"}, 305},
		{[]string{"Imágenes de e-mail enviadas"}, 25941},
		{[]string{"No existe"}, 0},
	}

	for _, c := range cases {
		got := extractXeroxVal(htmlXeroxES, c.keywords)
		if got != c.want {
			t.Errorf("extractXeroxVal(%v) = %d, want %d", c.keywords, got, c.want)
		}
	}
}

func TestExtractXeroxVal_EN(t *testing.T) {
	got := extractXeroxVal(htmlXeroxEN, []string{"Total Impressions"})
	if got != 100000 {
		t.Errorf("got %d, want 100000", got)
	}
	got = extractXeroxVal(htmlXeroxEN, []string{"Email Images Sent"})
	if got != 5000 {
		t.Errorf("got %d, want 5000", got)
	}
}

func TestExtractXeroxVal_FallbackKeyword(t *testing.T) {
	// La primera keyword no existe, debe encontrar la segunda.
	got := extractXeroxVal(htmlXeroxEN, []string{"Total de impresiones", "Total Impressions"})
	if got != 100000 {
		t.Errorf("fallback keyword: got %d, want 100000", got)
	}
}

func TestExtractXeroxVal_Nbsp(t *testing.T) {
	got := extractXeroxVal(htmlXeroxNbsp, []string{"Total de impresiones"})
	if got != 500 {
		t.Errorf("nbsp normalization: got %d, want 500", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests de parseXeroxHTML
// ─────────────────────────────────────────────────────────────────────────────

func TestParseXeroxHTML_ES(t *testing.T) {
	c, err := parseXeroxHTML("192.168.1.1", htmlXeroxES)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	check := func(name string, got, want int64) {
		t.Helper()
		if got != want {
			t.Errorf("%s: got %d, want %d", name, got, want)
		}
	}

	check("absolute.total", c.Absolute.Total, 238962)
	check("absolute.mono", c.Absolute.Mono, 215232)
	check("absolute.color", c.Absolute.Color, 23730)
	// print = printMono + printColor = 200000 + 15232
	check("by_function.print", c.LogicalMatrix.ByFunction.Print, 215232)
	// copy = copyMono + copyColor = 15232 + 8498
	check("by_function.copy", c.LogicalMatrix.ByFunction.Copy, 23730)
	check("by_mode.simplex", c.LogicalMatrix.ByMode.Simplex, 305)
	// duplex = 400 + 300 + 500 + 310
	check("by_mode.duplex", c.LogicalMatrix.ByMode.Duplex, 1510)
	check("by_destination.email", c.LogicalMatrix.ByDestination.Email, 25941)
	check("by_destination.others", c.LogicalMatrix.ByDestination.Others, 23056)
}

func TestParseXeroxHTML_EN(t *testing.T) {
	c, err := parseXeroxHTML("192.168.1.1", htmlXeroxEN)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Absolute.Total != 100000 {
		t.Errorf("absolute.total: got %d, want 100000", c.Absolute.Total)
	}
	if c.LogicalMatrix.ByFunction.Print != 60000 {
		t.Errorf("by_function.print: got %d, want 60000", c.LogicalMatrix.ByFunction.Print)
	}
	if c.LogicalMatrix.ByMode.Duplex != 750 {
		t.Errorf("by_mode.duplex: got %d, want 750 (200+100+300+150)", c.LogicalMatrix.ByMode.Duplex)
	}
}

func TestParseXeroxHTML_EmptyReturnsError(t *testing.T) {
	_, err := parseXeroxHTML("192.168.1.1", "<html><body>sin contadores</body></html>")
	if err == nil {
		t.Error("expected error for empty counters, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test del Fetcher completo (waterfall HTTP)
// ─────────────────────────────────────────────────────────────────────────────

func TestXeroxFetcher_Waterfall(t *testing.T) {
	// El primer endpoint falla (404), el segundo responde OK.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/counters/usage.php" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(htmlXeroxES))
	}))
	defer srv.Close()

	// Sustituimos SharedClient para apuntar al servidor de test.
	original := SharedClient
	SharedClient = srv.Client()
	defer func() { SharedClient = original }()

	// Extraemos host:port del servidor de test.
	host := srv.Listener.Addr().String()

	// El fetcher intenta http://{host}/counters/usage.php (404) y luego
	// http://{host}/stat/counters.php (200).
	f := &xeroxFetcher{}
	c, err := f.Fetch(host)
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if c.Absolute.Total != 238962 {
		t.Errorf("absolute.total: got %d, want 238962", c.Absolute.Total)
	}
}

func TestXeroxFetcher_InvalidIP(t *testing.T) {
	f := &xeroxFetcher{}
	_, err := f.Fetch("not-an-ip")
	if err == nil {
		t.Error("expected error for invalid IP, got nil")
	}
}
