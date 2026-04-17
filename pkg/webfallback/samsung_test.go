package webfallback

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Fixtures HTML
// ─────────────────────────────────────────────────────────────────────────────

// htmlLegacy simula billing_counters.htm con las dos formas que genera la impresora:
//   Forma A — HTML estático (Total, Copied, Printed, Platen)
//   Forma B — inyectado por JS en dos document.writeln (DADF)
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

// htmlSyncThru simula countersView.sws con las dos tablas matriciales.
// Mezcla deliberadamente las dos variantes de firmware que deben soportarse:
//   Variante A — valor numérico directo en el <td>
//   Variante B — valor envuelto en <span style='...'> (firmwares nuevos)
const htmlSyncThru = `
<html><body>
<table>
  <tr class='tonertable3_tr1'>
    <td>Nombre</td><td>Imprimir</td><td>Copiar</td><td>Impr. fax</td><td>Informe</td><td>Total</td>
  </tr>
  <tr id='swstable_counterTotalList_expandTR_2' class='tonertable3_tr2'  >
    <td height='25' width='25%' align='center' >Impresiones totales</td>
    <td height='25' width='15%' align='center' >215232</td>
    <td height='25' width='15%' align='center' >23513</td>
    <td height='25' width='15%' align='center' ><span style='text-align:center;'>0</span></td>
    <td height='25' width='15%' align='center' >217</td>
    <td height='25' width='15%' align='center' >238962</td>
  </tr>
</table>
<table>
  <tr class='tonertable3_tr1'>
    <td>Tipo</td><td>Email</td><td>FTP</td><td>SMB</td><td>USB</td><td>PC</td><td>Total</td>
  </tr>
  <tr id='swstable_counterSendList_expandTR_0' class='tonertable3_tr2' >
    <td height='25' width='14%' align='center' >Páginas</td>
    <td height='25' width='17%' align='center' >25941</td>
    <td height='25' width='14%' align='center' ><span style='text-align:center;'>0</span></td>
    <td height='25' width='13%' align='center' >0</td>
    <td height='25' width='13%' align='center' >11363</td>
    <td height='25' width='17%' align='center' >23056</td>
    <td height='25' width='13%' align='center' >60360</td>
  </tr>
</table>
</body></html>
`

// ─────────────────────────────────────────────────────────────────────────────
// Tests de integración (Fetch end-to-end vía httptest)
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

	assertCounters(t, c, countersWant{
		Total: 42318, Copy: 15136, Print: 27182,
		DADF: 8994, Platen: 1024, Scan: 10018,
	})
}

func TestFetch_SyncThruEndpoint(t *testing.T) {
	// Solo responde en la ruta nueva — la legacy devuelve 404.
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
	})
}

func TestFetch_WaterfallFallsThrough(t *testing.T) {
	// Los dos endpoints modernos devuelven 404; el SyncThru responde con datos.
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
	if c.TotalPages != 238962 {
		t.Errorf("TotalPages: got %d, want 238962", c.TotalPages)
	}
	// Los dos primeros endpoints modernos deben haberse intentado y fallado.
	if calls["/sws/app/information/counters/counters.json"] != 1 {
		t.Error("counters.json debió intentarse exactamente una vez")
	}
	if calls["/sws/app/information/counters/counters.html"] != 1 {
		t.Error("counters.html debió intentarse exactamente una vez")
	}
	// El SyncThru debió responder y detener el waterfall.
	if calls["/sws.application/information/countersView.sws"] != 1 {
		t.Error("countersView.sws debió intentarse exactamente una vez")
	}
	// El legacy NO debe haberse intentado (el waterfall se detuvo antes).
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

// ─────────────────────────────────────────────────────────────────────────────
// Tests del parser Modern JSON
// ─────────────────────────────────────────────────────────────────────────────

// jsRawObject simula el cuerpo de counters.json: objeto JS con claves sin comillas.
const jsRawObject = `{
  GXI_BILLING_TOTAL_IMP_CNT: 14695,
  GXI_BILLING_PRINT_TOTAL_IMP_CNT: 9800,
  GXI_BILLING_COPY_TOTAL_IMP_CNT: 4895,
  GXI_BILLING_SEND_TO_TOTAL_CNT: 3210
}`

// jsEmbeddedInHTML simula counters.html: mismas claves pero dentro de un <script>.
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
	// GXI_BILLING_TOTAL_IMP_CNT debe capturar 100, NO confundirse con
	// GXI_BILLING_PRINT_TOTAL_IMP_CNT (que contiene la misma subcadena).
	// COPY y SEND son relleno; el foco es que TOTAL (100) no se confunda con PRINT_TOTAL (999).
	body := `{ GXI_BILLING_PRINT_TOTAL_IMP_CNT: 999, GXI_BILLING_TOTAL_IMP_CNT: 100, GXI_BILLING_COPY_TOTAL_IMP_CNT: 50, GXI_BILLING_SEND_TO_TOTAL_CNT: 25 }`
	c, err := parseModernJSON("test", body)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if c.TotalPages != 100 {
		t.Errorf("TotalPages: got %d, want 100 (no debe confundirse con PRINT_TOTAL)", c.TotalPages)
	}
	if c.PrintPages != 999 {
		t.Errorf("PrintPages: got %d, want 999", c.PrintPages)
	}
}

func TestParseModernJSON_MissingKey(t *testing.T) {
	_, err := parseModernJSON("test", `{ GXI_BILLING_TOTAL_IMP_CNT: 1 }`)
	if err == nil {
		t.Fatal("esperaba error de campos faltantes, got nil")
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
	// Verifica el orden: counters.json debe intentarse ANTES que .sws y .htm
	order := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, r.URL.Path)
		switch r.URL.Path {
		case "/sws/app/information/counters/counters.json":
			_, _ = fmt.Fprint(w, jsRawObject)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	_, err := Get("samsung", hostOf(srv))
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if len(order) != 1 || order[0] != "/sws/app/information/counters/counters.json" {
		t.Errorf("se esperaba solo counters.json como primer intento, got: %v", order)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests unitarios del parser Legacy
// ─────────────────────────────────────────────────────────────────────────────

func TestParseLegacy_OK(t *testing.T) {
	c, err := parseLegacyHTML("test", htmlLegacy)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	assertCounters(t, c, countersWant{
		Total: 42318, Copy: 15136, Print: 27182,
		DADF: 8994, Platen: 1024, Scan: 10018,
	})
}

func TestParseLegacy_DADFViaDocumentWriteln(t *testing.T) {
	// Reproduce el bug original: DADF inyectado por JS en dos writeln separados.
	// El regex anterior (con </td>\s*<td) devolvía 0 aquí.
	html := `<script>` +
		`document.writeln("<td class='plainFont'>DADF Scan Page Count&nbsp;:</td>");` + "\n" +
		`document.writeln("<td class='valueFont'>8994 Page(s)<td>");` +
		`</script>`

	got, err := legacyExtract(legacyReDADFScans, html)
	if err != nil || got != 8994 {
		t.Errorf("DADF via document.writeln: got %d err %v; want 8994 nil", got, err)
	}
}

func TestParseLegacy_ThousandSeparators(t *testing.T) {
	html := `<td class="plainFont">Total Impressions:</td><td class="valueFont">1,234,567</td>`
	got, err := legacyExtract(legacyReTotalPages, html)
	if err != nil || got != 1234567 {
		t.Errorf("got %d err %v; want 1234567 nil", got, err)
	}
}

func TestParseLegacy_SingleQuotes(t *testing.T) {
	html := `<td class='plainFont'>Copied Sheets :</td><td class='valueFont'>999</td>`
	got, err := legacyExtract(legacyReCopyPages, html)
	if err != nil || got != 999 {
		t.Errorf("got %d err %v; want 999 nil", got, err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests unitarios del parser SyncThru
// ─────────────────────────────────────────────────────────────────────────────

func TestParseSyncThru_OK(t *testing.T) {
	c, err := parseSyncThruHTML("test", htmlSyncThru)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	assertCounters(t, c, countersWant{
		Total: 238962, Copy: 23513, Print: 215232, Scan: 60360,
	})
}

func TestParseSyncThru_RowNotFound(t *testing.T) {
	_, err := parseSyncThruHTML("test", "<html><body>sin datos</body></html>")
	if err == nil {
		t.Fatal("esperaba error de parse parcial cuando no hay filas")
	}
}

func TestParseSyncThru_PaginasSinTilde(t *testing.T) {
	// Algunos firmwares emiten "Paginas" sin tilde.
	html := strings.ReplaceAll(htmlSyncThru, "Páginas", "Paginas")
	c, err := parseSyncThruHTML("test", html)
	if err != nil {
		t.Fatalf("error inesperado con 'Paginas' sin tilde: %v", err)
	}
	if c.ScanPages != 60360 {
		t.Errorf("ScanPages: got %d, want 60360", c.ScanPages)
	}
}

func TestStExtractRowNums_SpanWrapped(t *testing.T) {
	// Reproduce la variante B real: valores envueltos en <span style='...'>.
	// El regex anterior (<td[^>]*>\s*([\d,]+)\s*</td>) devolvía 0 aquí.
	html := `<tr id='swstable_counterTotalList_expandTR_2' class='tonertable3_tr2'>
    <td height='25' width='25%' align='center' >Impresiones totales</td>
    <td height='25' width='15%' align='center' >215232</td>
    <td height='25' width='75%' align='center' ><span style='text-align:center;'>0</span></td>
    <td height='25' width='15%' align='center' >217</td>
    <td height='25' width='15%' align='center' >238962</td>
  </tr>`

	nums := stExtractRowNums(stReImprRow, html)
	// Se esperan 4 columnas numéricas (la etiqueta "Impresiones totales" no coincide).
	if len(nums) < 4 {
		t.Fatalf("se esperaban ≥4 nums, got %d: %v", len(nums), nums)
	}
	if nums[1] != 0 {
		t.Errorf("columna span (índice 1): got %d, want 0", nums[1])
	}
	if nums[len(nums)-1] != 238962 {
		t.Errorf("última columna (Total): got %d, want 238962", nums[len(nums)-1])
	}
}

func TestStExtractRowNums_ImpresionesTotales(t *testing.T) {
	nums := stExtractRowNums(stReImprRow, htmlSyncThru)
	want := []int64{215232, 23513, 0, 217, 238962}
	if len(nums) != len(want) {
		t.Fatalf("len: got %d, want %d — %v", len(nums), len(want), nums)
	}
	for i, w := range want {
		if nums[i] != w {
			t.Errorf("nums[%d]: got %d, want %d", i, nums[i], w)
		}
	}
}

func TestStExtractRowNums_SendUsage(t *testing.T) {
	nums := stExtractRowNums(stReSendRow, htmlSyncThru)
	if len(nums) < 1 {
		t.Fatal("no se extrajeron columnas de la fila de envíos")
	}
	if last := nums[len(nums)-1]; last != 60360 {
		t.Errorf("última columna (total scans): got %d, want 60360", last)
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
		t.Error("'Samsung' (mayúscula) debe estar registrado — el registro es case-insensitive")
	}
	if Supported("ricoh") {
		t.Error("'ricoh' no debe estar registrado aún")
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

type countersWant struct {
	Total, Copy, Print, DADF, Platen, Scan int64
}

func assertCounters(t *testing.T, c *Counters, w countersWant) {
	t.Helper()
	check := func(name string, got, want int64) {
		if got != want {
			t.Errorf("%s: got %d, want %d", name, got, want)
		}
	}
	check("TotalPages",  c.TotalPages,  w.Total)
	check("CopyPages",   c.CopyPages,   w.Copy)
	check("PrintPages",  c.PrintPages,  w.Print)
	check("DADFScans",   c.DADFScans,   w.DADF)
	check("PlatenScans", c.PlatenScans, w.Platen)
	check("ScanPages",   c.ScanPages,   w.Scan)
}
