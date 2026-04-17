package webfallback

// samsung.go — Implementación web-fallback para impresoras Samsung.
//
// Tres generaciones de firmware, un patrón Waterfall:
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ Endpoint                                           Parser               │
// ├──────────────────────────────────────────────────────────────────────────┤
// │ /sws/app/information/counters/counters.json        parseModernJSON      │
// │ /sws/app/information/counters/counters.html        parseModernJSON      │
// │   Objeto JS sin comillas (GXI_BILLING_*: 14695)                        │
// ├──────────────────────────────────────────────────────────────────────────┤
// │ /sws.application/information/countersView.sws      parseSyncThruHTML   │
// │   Tablas matriciales con ID swstable_counter*_contentTB                │
// ├──────────────────────────────────────────────────────────────────────────┤
// │ /Information/billing_counters.htm                  parseLegacyHTML     │
// │   Celdas class="valueFont", a veces inyectadas por JS                  │
// └──────────────────────────────────────────────────────────────────────────┘

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

func init() {
	Register("samsung", &samsungFetcher{})
}

// ─────────────────────────────────────────────────────────────────────────────
// Tabla de endpoints (Waterfall)
// ─────────────────────────────────────────────────────────────────────────────

type samsungEndpoint struct {
	path  string
	parse func(ip, body string) (*Counters, error)
}

var samsungEndpoints = []samsungEndpoint{
	{path: "/sws/app/information/counters/counters.json", parse: parseModernJSON},
	{path: "/sws/app/information/counters/counters.html", parse: parseModernJSON},
	{path: "/sws.application/information/countersView.sws", parse: parseSyncThruHTML},
	{path: "/Information/billing_counters.htm", parse: parseLegacyHTML},
}

type samsungFetcher struct{}

func (s *samsungFetcher) Fetch(ip string) (*Counters, error) {
	if err := validateIP(ip); err != nil {
		return nil, err
	}
	var lastErr error
	for _, ep := range samsungEndpoints {
		url := fmt.Sprintf("http://%s%s", ip, ep.path)
		body, err := samsungGet(url)
		if err != nil {
			fmt.Printf("[samsung][WEB] ⚠️  %s → %v\n", ep.path, err)
			lastErr = err
			continue
		}
		fmt.Printf("[samsung][WEB] ✅ HTTP 200 en %s — parseando\n", ep.path)
		c, parseErr := ep.parse(ip, body)
		return c, parseErr
	}
	return nil, fmt.Errorf("todos los endpoints fallaron para %s: %w", ip, lastErr)
}

func samsungGet(url string) (string, error) {
	resp, err := SharedClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	return string(body), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Parser A: Legacy — /Information/billing_counters.htm
// ─────────────────────────────────────────────────────────────────────────────
//
// HTML con celdas class="valueFont". El DADF puede venir inyectado por JS:
//   document.writeln("<td class='valueFont'>8994 Page(s)<td>");

func parseLegacyHTML(ip, html string) (*Counters, error) {
	c := &Counters{}

	extract := func(label string) int64 {
		re := regexp.MustCompile(
			`(?i)` + regexp.QuoteMeta(label) +
				`[\s\S]{0,120}?class=["']valueFont["'][^>]*>\s*([\d,.]+)`)
		m := re.FindStringSubmatch(html)
		if len(m) > 1 {
			v, _ := parseNum(m[1])
			return v
		}
		return 0
	}

	dadf   := extract("DADF Scan Page Count")
	platen := extract("Platen Scan Page Count")

	c.Absolute.Total                      = extract("Total Impressions")
	c.LogicalMatrix.ByFunction.Copy       = extract("Copied Sheets")
	c.LogicalMatrix.ByFunction.Print      = extract("Printed Sheets")
	c.HardwareUsage.TotalScans            = dadf + platen
	c.LogicalMatrix.ByDestination.TotalSend = dadf + platen

	return c, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Parser B: SyncThru — /sws.application/information/countersView.sws
// ─────────────────────────────────────────────────────────────────────────────
//
// El HTML contiene dos tablas identificadas por ID:
//
//   <table id='swstable_counterTotalList_contentTB'>
//     <!-- filas: Mono Simple (idx 0-4), Dúplex (idx 5-9), Totales (idx 10-14) -->
//     <!-- columnas por fila: Imprimir, Copiar, Fax, Informe, Total           -->
//   </table>
//
//   <table id='swstable_counterSendList_contentTB'>
//     <!-- una fila "Páginas": Email, FTP, SMB, USB, PC/Otros, Total          -->
//   </table>
//
// extractAllNumbers() aspira todos los números entre tags de ambas tablas.

func parseSyncThruHTML(ip, html string) (*Counters, error) {
	c := &Counters{}

	// ── Tabla de totales ─────────────────────────────────────────────────────
	tableTotalRe := regexp.MustCompile(
		`(?i)(?s)id=['"]swstable_counterTotalList_contentTB['"].*?</table>`)

	if match := tableTotalRe.FindString(html); match != "" {
		nums := extractAllNumbers(match)
		// Layout esperado (15 valores = 3 filas × 5 columnas):
		//   idx  0- 4 → Mono Simple:         Impr, Copy, Fax, Inf, Total
		//   idx  5- 9 → Dúplex:              Impr, Copy, Fax, Inf, Total
		//   idx 10-14 → Impresiones Totales: Impr, Copy, Fax, Inf, Total
		if len(nums) >= 15 {
			c.LogicalMatrix.ByMode.Simplex       = nums[4]  // Mono Simple → Total
			c.LogicalMatrix.ByMode.Duplex        = nums[9]  // Dúplex → Total
			c.LogicalMatrix.ByFunction.Print     = nums[10] // Totales → Imprimir
			c.LogicalMatrix.ByFunction.Copy      = nums[11] // Totales → Copiar
			c.LogicalMatrix.ByFunction.FaxPrint  = nums[12] // Totales → Fax
			c.LogicalMatrix.ByFunction.Reports   = nums[13] // Totales → Informe
			c.Absolute.Total                     = nums[14] // Totales → Total
		}
	}

	// ── Tabla de envíos ──────────────────────────────────────────────────────
	tableSendRe := regexp.MustCompile(
		`(?i)(?s)id=['"]swstable_counterSendList_contentTB['"].*?</table>`)

	if match := tableSendRe.FindString(html); match != "" {
		nums := extractAllNumbers(match)
		// Layout esperado (6 valores = 1 fila × 6 columnas):
		//   Email, FTP, SMB, USB, Otros/PC, Total
		n := len(nums)
		if n >= 1 {
			c.HardwareUsage.TotalScans               = nums[n-1]
			c.LogicalMatrix.ByDestination.TotalSend  = nums[n-1]
		}
		if n >= 6 {
			c.LogicalMatrix.ByDestination.Email  = nums[0]
			c.LogicalMatrix.ByDestination.FTP    = nums[1]
			c.LogicalMatrix.ByDestination.SMB    = nums[2]
			c.LogicalMatrix.ByDestination.USB    = nums[3]
			c.LogicalMatrix.ByDestination.Others = nums[4]
		}
	}

	if c.Absolute.Total == 0 {
		return c, fmt.Errorf("syncthru: no se encontraron contadores en el HTML de %s", ip)
	}
	return c, nil
}

// extractAllNumbers extrae todos los enteros que aparecen entre tags HTML
// (patrón >número<). Tolerante a comas de miles y puntos de separación.
func extractAllNumbers(text string) []int64 {
	re := regexp.MustCompile(`>\s*([\d,.]+)\s*<`)
	var nums []int64
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		if v, err := parseNum(m[1]); err == nil {
			nums = append(nums, v)
		}
	}
	return nums
}

// ─────────────────────────────────────────────────────────────────────────────
// Parser C: Modern JSON — /sws/app/information/counters/counters.{json,html}
// ─────────────────────────────────────────────────────────────────────────────
//
// El body es un objeto JavaScript con claves sin comillas (JSON inválido):
//   { GXI_BILLING_TOTAL_IMP_CNT: 14695, GXI_BILLING_PRINT_TOTAL_IMP_CNT: 9800 }
//
// También puede venir incrustado en HTML dentro de un <script>.
// Se usa regexp para extraer cada par clave:valor directamente del texto.

func parseModernJSON(ip, body string) (*Counters, error) {
	c := &Counters{}

	getVal := func(key string) int64 {
		// \b (word boundary) evita que TOTAL_IMP_CNT coincida con PRINT_TOTAL_IMP_CNT.
		re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(key) + `\b\s*:\s*(\d+)`)
		m := re.FindStringSubmatch(body)
		if len(m) > 1 {
			v, _ := strconv.ParseInt(m[1], 10, 64)
			return v
		}
		return 0
	}

	c.Absolute.Total                      = getVal("GXI_BILLING_TOTAL_IMP_CNT")
	c.LogicalMatrix.ByFunction.Print      = getVal("GXI_BILLING_PRINT_TOTAL_IMP_CNT")
	c.LogicalMatrix.ByFunction.Copy       = getVal("GXI_BILLING_COPY_TOTAL_IMP_CNT")
	c.HardwareUsage.TotalScans            = getVal("GXI_BILLING_SEND_TO_TOTAL_CNT")
	c.LogicalMatrix.ByDestination.TotalSend = c.HardwareUsage.TotalScans
	c.LogicalMatrix.ByMode.Duplex         = getVal("GXI_BILLING_DUPLEX_BW_TOTAL_CNT")
	c.LogicalMatrix.ByMode.Simplex        = getVal("GXI_BILLING_SIMPLEX_BW_TOTAL_CNT")

	if c.Absolute.Total == 0 {
		return nil, fmt.Errorf("modern: no se encontraron contadores básicos en %s", ip)
	}
	return c, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Utilidad compartida
// ─────────────────────────────────────────────────────────────────────────────

// parseNum convierte "1,024" o "238.962" o "238962" en int64.
// Elimina tanto comas como puntos (ambos usados como separadores de miles
// según la configuración regional del firmware).
func parseNum(s string) (int64, error) {
	clean := strings.NewReplacer(",", "", ".", "").Replace(s)
	return strconv.ParseInt(clean, 10, 64)
}
