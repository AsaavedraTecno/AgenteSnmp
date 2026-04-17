package webfallback

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Fixtures HTML
// ─────────────────────────────────────────────────────────────────────────────

// htmlLegacy simula billing_counters.htm.
// El DADF viene inyectado por JS en dos document.writeln (Forma B).
const htmlLegacy = `
<html><body><table>
  <tr>
    <td class="plainFont">&nbsp;&nbsp;&nbsp; Total Impressions &nbsp;:</td>
    <td class="valueFont">42,318</td>
  </tr>
  <tr>
    <td class="plainFont">&nbsp;&nbsp;&nbsp; Copied Sheets &nbsp;:</td>
    <td class="valueFont">15136</td>
  </tr>
  <tr>
    <td class="plainFont">&nbsp;&nbsp;&nbsp; Printed Sheets &nbsp;:</td>
    <td class="valueFont">27182</td>
  </tr>
  <script>
  document.writeln("<tr><td class='plainFont'> &nbsp;&nbsp;&nbsp; DADF Scan Page Count&nbsp;:</td>");
  document.writeln("<td class='valueFont'>8994 Page(s)<td></tr>");
  </script>
  <tr><td class='plainFont'> &nbsp;&nbsp;&nbsp; Platen Scan Page Count&nbsp;:</td>
  <td class='valueFont'>1,024 Page(s)<td></tr>
</table></body></html>
`

// htmlSyncThru simula countersView.sws con los IDs de tabla reales.
//
// TotalList — 3 filas × 5 cols = 15 números:
//   Fila 0 (Mono Simple):         100,  200, 0,   5,   305    → idx 0-4
//   Fila 1 (Dúplex):             1000,  500, 0,  10,  1510    → idx 5-9
//   Fila 2 (Impresiones Totales): 215232, 23513, 0, 217, 238962 → idx 10-14
//
// SendList — 1 fila × 6 cols:
//   Email=25941, FTP=0, SMB=0, USB=11363, Otros=23056, Total=60360
const htmlSyncThru = `
<html><body>
<table id='swstable_counterTotalList_contentTB'>
  <tr><th>Modo</th><th>Imprimir</th><th>Copiar</th><th>Fax</th><th>Informe</th><th>Total</th></tr>
  <tr><td>Mono Simple</td><td>100</td><td>200</td><td>0</td><td>5</td><td>305</td></tr>
  <tr><td>Dúplex</td><td>1000</td><td>500</td><td>0</td><td>10</td><td>1510</td></tr>
  <tr><td>Impresiones Totales</td>
    <td>215232</td><td>23513</td><td>0</td><td>217</td><td>238962</td>
  </tr>
</table>
<table id='swstable_counterSendList_contentTB'>
  <tr><th>Tipo</th><th>Email</th><th>FTP</th><th>SMB</th><th>USB</th><th>Otros</th><th>Total</th></tr>
  <tr><td>Páginas</td>
    <td>25941</td><td>0</td><td>0</td><td>11363</td><td>23056</td><td>60360</td>
  </tr>
</table>
</body></html>
`

// jsRawObject simula counters.json: objeto JS con claves sin comillas.
const jsRawObject = `{
  GXI_BILLING_TOTAL_IMP_CNT: 14695,
  GXI_BILLING_PRINT_TOTAL_IMP_CNT: 9800,
  GXI_BILLING_COPY_TOTAL_IMP_CNT: 4895,
  GXI_BILLING_SEND_TO_TOTAL_CNT: 3210
}`

// jsEmbeddedInHTML simula counters.html: mismas claves dentro de un <script>.
const jsEmbeddedInHTML = `<html><body>
<script type="text/javascript">
var billingData = {
  GXI_BILLING_TOTAL_IMP_CNT: 14695,
  GXI_BILLING_PRINT_TOTAL_IMP_CNT: 9800,
  GXI_BILLING_COPY_TOTAL_IMP_CNT: 4895,
  GXI_BILLING_SEND_TO_TOTAL_CNT: 3210
};
</script>
</body></html>`

// ─────────────────────────────────────────────────────────────────────────────
// Tests de integración — Fetch end-to-end vía httptest
// ─────────────────────────────────────────────────────────────────────────────

func TestFetch_LegacyEndpoint(t *testing.T) {
	srv := newSamsungServer(map[string]string{
		"/Information/billing_counters.htm": htmlLegacy,
	})
	defer srv.Close()

	c, err := Get("samsung", hostOf(srv))
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	// DADF(8994) + Platen(1024) = 10018
	assertCounters(t, c, countersWant{Total: 42318, Copy: 15136, Print: 27182, Scan: 10018})
}

func TestFetch_SyncThruEndpoint(t *testing.T) {
	srv := newSamsungServer(map[string]string{
		"/sws.application/information/countersView.sws": htmlSyncThru,
	})
	defer srv.Close()

	c, err := Get("samsung", hostOf(srv))
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	assertCounters(t, c, countersWant{
		Total: 238962, Copy: 23513, Print: 215232, Scan: 60360,
		Simplex: 305, Duplex: 1510,
	})
}

func TestFetch_WaterfallFallsThrough(t *testing.T) {
	// Los dos modernos devuelven 404; SyncThru responde.
	// Verifica que el waterfall recorre en orden y se detiene al primer 200.
	calls := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		if r.URL.Path == "/sws.application/information/countersView.sws" {
			_, _ = fmt.Fprint(w, htmlSyncThru)
		} else {
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := Get("samsung", hostOf(srv))
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if c.Absolute.Total != 238962 {
		t.Errorf("Absolute.Total: got %d, want 238962", c.Absolute.Total)
	}
	if calls["/sws/app/information/counters/counters.json"] != 1 {
		t.Error("counters.json debió intentarse exactamente una vez")
	}
	if calls["/sws/app/information/counters/counters.html"] != 1 {
		t.Error("counters.html debió intentarse exactamente una vez")
	}
	if calls["/sws.application/information/countersView.sws"] != 1 {
		t.Error("countersView.sws debió intentarse exactamente una vez")
	}
	if calls["/Information/billing_counters.htm"] != 0 {
		t.Error("billing_counters.htm NO debió intentarse (waterfall ya encontró datos)")
	}
}

func TestFetch_AllEndpointsFail(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	_, err := Get("samsung", hostOf(srv))
	if err == nil {
		t.Fatal("esperaba error cuando todos los endpoints fallan")
	}
}

func TestFetch_ModernJSONEndpoint(t *testing.T) {
	srv := newSamsungServer(map[string]string{
		"/sws/app/information/counters/counters.json": jsRawObject,
	})
	defer srv.Close()

	c, err := Get("samsung", hostOf(srv))
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	assertCounters(t, c, countersWant{Total: 14695, Print: 9800, Copy: 4895, Scan: 3210})
}

func TestFetch_WaterfallModernBeforeLegacy(t *testing.T) {
	order := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, r.URL.Path)
		if r.URL.Path == "/sws/app/information/counters/counters.json" {
			_, _ = fmt.Fprint(w, jsRawObject)
		} else {
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	_, err := Get("samsung", hostOf(srv))
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if len(order) != 1 || order[0] != "/sws/app/information/counters/counters.json" {
		t.Errorf("primer intento debe ser counters.json, got: %v", order)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests unitarios — parser Legacy
// ─────────────────────────────────────────────────────────────────────────────

func TestParseLegacy_OK(t *testing.T) {
	c, err := parseLegacyHTML("test", htmlLegacy)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	assertCounters(t, c, countersWant{Total: 42318, Copy: 15136, Print: 27182, Scan: 10018})
}

func TestParseLegacy_DADFViaDocumentWriteln(t *testing.T) {
	// Bug original: DADF inyectado por JS en dos writeln separados.
	html := `<script>` +
		`document.writeln("<td class='plainFont'>DADF Scan Page Count&nbsp;:</td>");` + "\n" +
		`document.writeln("<td class='valueFont'>8994 Page(s)<td>");` +
		`</script>` +
		`<td class='plainFont'>Platen Scan Page Count&nbsp;:</td>` +
		`<td class='valueFont'>1000 Page(s)</td>`

	c, _ := parseLegacyHTML("test", html)
	if c.HardwareUsage.TotalScans != 9994 { // 8994 + 1000
		t.Errorf("TotalScans: got %d, want 9994", c.HardwareUsage.TotalScans)
	}
}

func TestParseLegacy_ThousandSeparators(t *testing.T) {
	html := `<td class="plainFont">Total Impressions:</td><td class="valueFont">1,234,567</td>`
	c, _ := parseLegacyHTML("test", html)
	if c.Absolute.Total != 1234567 {
		t.Errorf("Absolute.Total: got %d, want 1234567", c.Absolute.Total)
	}
}

func TestParseLegacy_SingleQuotes(t *testing.T) {
	html := `<td class='plainFont'>Copied Sheets :</td><td class='valueFont'>999</td>`
	c, _ := parseLegacyHTML("test", html)
	if c.LogicalMatrix.ByFunction.Copy != 999 {
		t.Errorf("ByFunction.Copy: got %d, want 999", c.LogicalMatrix.ByFunction.Copy)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests unitarios — parser SyncThru
// ─────────────────────────────────────────────────────────────────────────────

func TestParseSyncThru_OK(t *testing.T) {
	c, err := parseSyncThruHTML("test", htmlSyncThru)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	assertCounters(t, c, countersWant{
		Total: 238962, Copy: 23513, Print: 215232, Scan: 60360,
		Simplex: 305, Duplex: 1510,
	})
}

func TestParseSyncThru_ByDestination(t *testing.T) {
	c, err := parseSyncThruHTML("test", htmlSyncThru)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	dst := c.LogicalMatrix.ByDestination
	if dst.Email != 25941 {
		t.Errorf("Email: got %d, want 25941", dst.Email)
	}
	if dst.USB != 11363 {
		t.Errorf("USB: got %d, want 11363", dst.USB)
	}
	if dst.Others != 23056 {
		t.Errorf("Others: got %d, want 23056", dst.Others)
	}
	if dst.TotalSend != 60360 {
		t.Errorf("TotalSend: got %d, want 60360", dst.TotalSend)
	}
}

func TestParseSyncThru_EmptyHTML_ReturnsError(t *testing.T) {
	_, err := parseSyncThruHTML("test", "<html><body>sin datos</body></html>")
	if err == nil {
		t.Fatal("esperaba error cuando no hay tablas con IDs conocidos")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests unitarios — extractAllNumbers
// ─────────────────────────────────────────────────────────────────────────────

func TestExtractAllNumbers_BasicRow(t *testing.T) {
	html := `<tr><td>Etiqueta</td><td>215232</td><td>23513</td><td>0</td><td>238962</td></tr>`
	nums := extractAllNumbers(html)
	want := []int64{215232, 23513, 0, 238962}
	if len(nums) != len(want) {
		t.Fatalf("len: got %d, want %d — %v", len(nums), len(want), nums)
	}
	for i, w := range want {
		if nums[i] != w {
			t.Errorf("nums[%d]: got %d, want %d", i, nums[i], w)
		}
	}
}

func TestExtractAllNumbers_ThousandSeparators(t *testing.T) {
	html := `<td>1,234,567</td>`
	nums := extractAllNumbers(html)
	if len(nums) != 1 || nums[0] != 1234567 {
		t.Errorf("got %v, want [1234567]", nums)
	}
}

func TestExtractAllNumbers_FullTotalTable(t *testing.T) {
	// 15 números en la tabla de totales: 3 filas × 5 cols
	re := regexp.MustCompile(`(?i)(?s)id=['"]swstable_counterTotalList_contentTB['"].*?</table>`)
	match := re.FindString(htmlSyncThru)
	if match == "" {
		t.Fatal("no se encontró la tabla TotalList en htmlSyncThru")
	}
	nums := extractAllNumbers(match)
	if len(nums) != 15 {
		t.Fatalf("se esperaban 15 números, got %d: %v", len(nums), nums)
	}
	// idx[4]=Simplex Total, idx[9]=Duplex Total, idx[10]=Print, idx[11]=Copy, idx[14]=Total
	cases := []struct{ idx int; want int64; label string }{
		{4,  305,    "ByMode.Simplex (idx 4)"},
		{9,  1510,   "ByMode.Duplex  (idx 9)"},
		{10, 215232, "Print          (idx 10)"},
		{11, 23513,  "Copy           (idx 11)"},
		{14, 238962, "Total          (idx 14)"},
	}
	for _, tc := range cases {
		if nums[tc.idx] != tc.want {
			t.Errorf("%s: got %d, want %d", tc.label, nums[tc.idx], tc.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests unitarios — parser Modern JSON
// ─────────────────────────────────────────────────────────────────────────────

func TestParseModernJSON_RawObject(t *testing.T) {
	c, err := parseModernJSON("test", jsRawObject)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	assertCounters(t, c, countersWant{Total: 14695, Print: 9800, Copy: 4895, Scan: 3210})
}

func TestParseModernJSON_EmbeddedInHTML(t *testing.T) {
	c, err := parseModernJSON("test", jsEmbeddedInHTML)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	assertCounters(t, c, countersWant{Total: 14695, Print: 9800, Copy: 4895, Scan: 3210})
}

func TestParseModernJSON_NoBoundaryConfusion(t *testing.T) {
	// GXI_BILLING_TOTAL_IMP_CNT (100) NO debe confundirse con
	// GXI_BILLING_PRINT_TOTAL_IMP_CNT (999) que contiene la misma subcadena.
	body := `{ GXI_BILLING_PRINT_TOTAL_IMP_CNT: 999, GXI_BILLING_TOTAL_IMP_CNT: 100,
	           GXI_BILLING_COPY_TOTAL_IMP_CNT: 50, GXI_BILLING_SEND_TO_TOTAL_CNT: 25 }`
	c, err := parseModernJSON("test", body)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if c.Absolute.Total != 100 {
		t.Errorf("Absolute.Total: got %d, want 100", c.Absolute.Total)
	}
	if c.LogicalMatrix.ByFunction.Print != 999 {
		t.Errorf("ByFunction.Print: got %d, want 999", c.LogicalMatrix.ByFunction.Print)
	}
}

func TestParseModernJSON_MissingKey_ReturnsError(t *testing.T) {
	// Sin GXI_BILLING_TOTAL_IMP_CNT el parser debe retornar error.
	_, err := parseModernJSON("test", `{ GXI_BILLING_PRINT_TOTAL_IMP_CNT: 1 }`)
	if err == nil {
		t.Fatal("esperaba error cuando falta la clave de total, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests del registro
// ─────────────────────────────────────────────────────────────────────────────

func TestSupported(t *testing.T) {
	if !Supported("samsung") {
		t.Error("'samsung' debe estar registrado")
	}
	if !Supported("Samsung") {
		t.Error("'Samsung' (mayúscula) debe ser case-insensitive")
	}
	if Supported("ricoh") {
		t.Error("'ricoh' no debe estar registrado")
	}
}

func TestGet_UnsupportedBrand(t *testing.T) {
	_, err := Get("ricoh", "192.168.1.1")
	if err == nil {
		t.Fatal("esperaba ErrNotSupported, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers de test
// ─────────────────────────────────────────────────────────────────────────────

func newSamsungServer(routes map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body, ok := routes[r.URL.Path]; ok {
			_, _ = fmt.Fprint(w, body)
			return
		}
		http.NotFound(w, r)
	}))
}

func hostOf(srv *httptest.Server) string {
	return strings.TrimPrefix(srv.URL, "http://")
}

// countersWant define los valores esperados para assertCounters.
// Los campos en cero se ignoran en la comparación (no se verifica que sean 0,
// simplemente no se comprueban) — solo se validan los campos explícitamente
// asignados en el literal del test.
type countersWant struct {
	Total, Copy, Print, Scan    int64
	Simplex, Duplex             int64
}

func assertCounters(t *testing.T, c *Counters, w countersWant) {
	t.Helper()
	chk := func(name string, got, want int64) {
		t.Helper()
		if want != 0 && got != want {
			t.Errorf("%s: got %d, want %d", name, got, want)
		}
	}
	chk("Absolute.Total",              c.Absolute.Total,                    w.Total)
	chk("LogicalMatrix.ByFunction.Copy",  c.LogicalMatrix.ByFunction.Copy,  w.Copy)
	chk("LogicalMatrix.ByFunction.Print", c.LogicalMatrix.ByFunction.Print, w.Print)
	chk("HardwareUsage.TotalScans",    c.HardwareUsage.TotalScans,          w.Scan)
	chk("LogicalMatrix.ByMode.Simplex", c.LogicalMatrix.ByMode.Simplex,     w.Simplex)
	chk("LogicalMatrix.ByMode.Duplex",  c.LogicalMatrix.ByMode.Duplex,      w.Duplex)
}
