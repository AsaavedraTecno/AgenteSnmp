package webfallback

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

type samsungEndpoint struct {
	path  string
	parse func(ip, html string) (*Counters, error)
}

var samsungEndpoints = []samsungEndpoint{
	{path: "/sws/app/information/counters/counters.json", parse: parseModernJSON},
	{path: "/sws/app/information/counters/counters.html", parse: parseModernJSON},
	{path: "/sws.application/information/countersView.sws", parse: parseSyncThruHTML},
	{path: "/Information/billing_counters.htm", parse: parseLegacyHTML},
}

type samsungFetcher struct{}

func (s *samsungFetcher) Fetch(ip string) (*Counters, error) {
	var lastErr error
	for _, ep := range samsungEndpoints {
		url := fmt.Sprintf("http://%s%s", ip, ep.path)
		body, err := samsungGet(url)
		if err != nil {
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
// PARSER B: SyncThru (EL QUE FALLABA) - Estrategia por IDs de Tabla
// ─────────────────────────────────────────────────────────────────────────────

func parseSyncThruHTML(ip, html string) (*Counters, error) {
	c := &Counters{}

	// Buscamos el cuerpo de la tabla de contadores por su ID único
	// Este ID contiene solo las filas de datos (Mono, Duplex, Total), sin encabezados.
	tableRe := regexp.MustCompile(`(?i)(?s)id=['"]swstable_counterTotalList_contentTB['"].*?</table>`)
	tableMatch := tableRe.FindString(html)

	if tableMatch != "" {
		// Extraemos TODOS los números de la tabla de datos
		nums := extractAllNumbers(tableMatch)

		// La tabla tiene 3 filas de 5 columnas cada una = 15 números.
		// Fila 0 (indices 0-4): Mono Simple
		// Fila 1 (indices 5-9): Duplex
		// Fila 2 (indices 10-14): Impresiones Totales

		if len(nums) >= 15 {
			c.SimplexMono = nums[4] // Total de la fila Mono Simple
			c.DuplexMono = nums[9]  // Total de la fila Duplex
			c.TotalPages = nums[14] // Total general de impresiones
			c.PrintPages = nums[10] // Imprimir (columna 1 de la fila total)
			c.CopyPages = nums[11]  // Copiar (columna 2 de la fila total)
		} else if len(nums) >= 5 {
			// Fallback si solo hay una fila: usamos la primera que encontremos
			c.TotalPages = nums[len(nums)-1]
			c.PrintPages = nums[0]
			c.CopyPages = nums[1]
		}
	} else {
		return nil, fmt.Errorf("no se encontró la tabla de contadores (swstable_counterTotalList)")
	}

	return c, nil
}

// extractAllNumbers aspira cualquier número contenido entre tags o valores en el bloque
func extractAllNumbers(text string) []int64 {
	// Buscamos números que estén precedidos por > y seguidos por < o espacios
	// Esto captura >123,456< de forma muy robusta
	re := regexp.MustCompile(`>\s*([\d,.]+)\s*<`)
	matches := re.FindAllStringSubmatch(text, -1)

	var nums []int64
	for _, m := range matches {
		if v, err := parseNum(m[1]); err == nil {
			nums = append(nums, v)
		}
	}
	return nums
}

// ─────────────────────────────────────────────────────────────────────────────
// PARSER C: Modern JSON (Arreglado typo de Scan)
// ─────────────────────────────────────────────────────────────────────────────

func parseModernJSON(ip, body string) (*Counters, error) {
	c := &Counters{}

	// Helper interno para regex simple
	getVal := func(key string) int64 {
		re := regexp.MustCompile(`(?i)` + key + `\b\s*:\s*(\d+)`)
		m := re.FindStringSubmatch(body)
		if len(m) > 1 {
			v, _ := strconv.ParseInt(m[1], 10, 64)
			return v
		}
		return 0
	}

	c.TotalPages = getVal("GXI_BILLING_TOTAL_IMP_CNT")
	c.PrintPages = getVal("GXI_BILLING_PRINT_TOTAL_IMP_CNT")
	c.CopyPages = getVal("GXI_BILLING_COPY_TOTAL_IMP_CNT")
	// CORREGIDO: Tenía una 'S' de más en el código anterior
	c.ScanPages = getVal("GXI_BILLING_SEND_TO_TOTAL_CNT")

	c.DuplexMono = getVal("GXI_BILLING_DUPLEX_BW_TOTAL_CNT")
	c.SimplexMono = getVal("GXI_BILLING_SIMPLEX_BW_TOTAL_CNT")

	if c.TotalPages == 0 {
		return nil, fmt.Errorf("modern: no se encontraron contadores básicos")
	}
	return c, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// PARSER A: Legacy (SCX-6545X)
// ─────────────────────────────────────────────────────────────────────────────

func parseLegacyHTML(ip, html string) (*Counters, error) {
	c := &Counters{}
	extract := func(label string) int64 {
		re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(label) + `[\s\S]{0,120}?class=["']valueFont["'][^>]*>\s*([\d,.]+)`)
		m := re.FindStringSubmatch(html)
		if len(m) > 1 {
			v, _ := parseNum(m[1])
			return v
		}
		return 0
	}

	c.TotalPages = extract("Total Impressions")
	c.CopyPages = extract("Copied Sheets")
	c.PrintPages = extract("Printed Sheets")
	c.DADFScans = extract("DADF Scan Page Count")
	c.PlatenScans = extract("Platen Scan Page Count")
	c.ScanPages = c.DADFScans + c.PlatenScans

	return c, nil
}

func parseNum(s string) (int64, error) {
	clean := strings.NewReplacer(",", "", ".", "").Replace(s)
	return strconv.ParseInt(clean, 10, 64)
}
