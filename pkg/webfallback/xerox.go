package webfallback

// xerox.go — Implementación web-fallback para impresoras Xerox AltaLink/VersaLink.
//
// Dos generaciones de firmware, patrón Waterfall:
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ Endpoint                      Parser                                    │
// ├──────────────────────────────────────────────────────────────────────────┤
// │ /counters/usage.php           parseXeroxHTML  (AltaLink / VersaLink)   │
// │ /stat/counters.php            parseXeroxHTML  (modelos legacy)          │
// └──────────────────────────────────────────────────────────────────────────┘

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

func init() {
	Register("xerox", &xeroxFetcher{})
}

// ─────────────────────────────────────────────────────────────────────────────
// Tabla de endpoints (Waterfall)
// ─────────────────────────────────────────────────────────────────────────────

type xeroxEndpoint struct {
	path  string
	parse func(ip, body string) (*Counters, error)
}

var xeroxEndpoints = []xeroxEndpoint{
	{path: "/counters/usage.php", parse: parseXeroxHTML},
	{path: "/stat/counters.php", parse: parseXeroxHTML},
}

type xeroxFetcher struct{}

func (x *xeroxFetcher) Fetch(ip string) (*Counters, error) {
	if err := validateIP(ip); err != nil {
		return nil, err
	}
	var lastErr error
	for _, ep := range xeroxEndpoints {
		url := fmt.Sprintf("http://%s%s", ip, ep.path)
		body, err := xeroxGet(url)
		if err != nil {
			fmt.Printf("[xerox][WEB] ⚠️  %s → %v\n", ep.path, err)
			lastErr = err
			continue
		}
		fmt.Printf("[xerox][WEB] ✅ HTTP 200 en %s — parseando\n", ep.path)
		c, parseErr := ep.parse(ip, body)
		return c, parseErr
	}
	return nil, fmt.Errorf("todos los endpoints fallaron para %s: %w", ip, lastErr)
}

func xeroxGet(url string) (string, error) {
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
// Parser: /counters/usage.php  y  /stat/counters.php
// ─────────────────────────────────────────────────────────────────────────────

func parseXeroxHTML(ip, html string) (*Counters, error) {
	c := &Counters{}

	val := func(keywords ...string) int64 {
		return extractXeroxVal(html, keywords)
	}

	// ── Absolutos ────────────────────────────────────────────────────────────
	c.Absolute.Total = val("Total de impresiones", "Total Impressions")
	c.Absolute.Mono = val("Impresiones en negro", "Black Impressions")
	c.Absolute.Color = val("Impresiones en color", "Color Impressions")

	// Validación: total debe ser igual a mono + color
	if c.Absolute.Total == 0 && (c.Absolute.Mono > 0 || c.Absolute.Color > 0) {
		c.Absolute.Total = c.Absolute.Mono + c.Absolute.Color
	}
	if c.Absolute.Color == 0 && c.Absolute.Total > 0 && c.Absolute.Mono > 0 {
		c.Absolute.Color = c.Absolute.Total - c.Absolute.Mono
	}

	// ── Por función: print y copy se calculan sumando sus componentes ────────
	printMono := val("Impresiones impresas en negro", "Black Print Impressions", "Black Printed Impressions")
	printColor := val("Impresiones impresas en color", "Color Print Impressions", "Color Printed Impressions")
	copyMono := val("Impresiones copiadas en negro", "Black Copy Impressions", "Black Copied Impressions")
	copyColor := val("Impresiones copiadas en color", "Color Copy Impressions", "Color Copied Impressions")

	c.LogicalMatrix.ByFunction.Print = printMono + printColor
	c.LogicalMatrix.ByFunction.Copy = copyMono + copyColor

	// Validación: copy + print debe ser igual al total.
	if c.Absolute.Total == 0 && (c.LogicalMatrix.ByFunction.Print > 0 || c.LogicalMatrix.ByFunction.Copy > 0) {
		c.Absolute.Total = c.LogicalMatrix.ByFunction.Print + c.LogicalMatrix.ByFunction.Copy
	}

	// ── Por modo ─────────────────────────────────────────────────────────────
	c.LogicalMatrix.ByMode.Simplex = val("Impresiones individuales", "1 Sided Impressions", "Single Impressions")

	// Dúplex: suma de todas las variantes "Hojas a dos caras" (copia e impresión, color y mono)
	// Xerox reporta Hojas (Sheets) para el dúplex, no impresiones. 1 Hoja Dúplex = 2 Impresiones.
	duplexCopyMono := val("Hojas copiadas a dos caras en negro", "2 Sided Black Copy Sheets", "Black Copied 2 Sided Sheets")
	duplexCopyColor := val("Hojas copiadas a dos caras en color", "2 Sided Color Copy Sheets", "Color Copied 2 Sided Sheets")
	duplexPrintMono := val("Hojas impresas a dos caras en negro", "2 Sided Black Print Sheets", "Black Printed 2 Sided Sheets")
	duplexPrintColor := val("Hojas impresas a dos caras en color", "2 Sided Color Print Sheets", "Color Printed 2 Sided Sheets")

	hojasDuplexTotales := duplexCopyMono + duplexCopyColor + duplexPrintMono + duplexPrintColor
	c.LogicalMatrix.ByMode.Duplex = hojasDuplexTotales * 2
	c.LogicalMatrix.ByMode.Simplex = c.Absolute.Total - c.LogicalMatrix.ByMode.Duplex
	if c.LogicalMatrix.ByMode.Simplex < 0 {
		c.LogicalMatrix.ByMode.Simplex = 0
	}

	// ── Por destino ──────────────────────────────────────────────────────────
	c.LogicalMatrix.ByDestination.Email = val("Imágenes de e-mail enviadas", "Email Images Sent")
	c.LogicalMatrix.ByDestination.Others = val("Imágenes de escaneado en red enviadas", "Network Scanning Images Sent")
	c.LogicalMatrix.ByDestination.TotalSend = c.LogicalMatrix.ByDestination.Email + c.LogicalMatrix.ByDestination.Others

	// ── Hardware Usage ───────────────────────────────────────────────────────
	c.HardwareUsage.TotalScans = c.LogicalMatrix.ByDestination.TotalSend

	if c.Absolute.Total == 0 && c.HardwareUsage.TotalScans == 0 {
		return c, fmt.Errorf("xerox: no se encontraron contadores en el HTML de %s", ip)
	}
	return c, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Helper: extractXeroxVal
// ─────────────────────────────────────────────────────────────────────────────

// extractXeroxVal busca la primera keyword que aparezca en el HTML normalizado
// y devuelve el número de la siguiente celda <td>. Devuelve 0 si no hay match.
func extractXeroxVal(html string, keywords []string) int64 {
	// Normalizar: &nbsp; → espacio, colapsar espacios múltiples.
	normalized := strings.ReplaceAll(html, "&nbsp;", " ")
	spaceRe := regexp.MustCompile(`\s{2,}`)
	normalized = spaceRe.ReplaceAllString(normalized, " ")

	for _, kw := range keywords {
		re := regexp.MustCompile(
			`(?is)` + regexp.QuoteMeta(kw) + `.*?<td[^>]*>\s*([\d,.]+)\s*</td>`)
		if m := re.FindStringSubmatch(normalized); len(m) > 1 {
			v, err := parseNum(m[1])
			if err == nil {
				return v
			}
		}
	}
	return 0
}
