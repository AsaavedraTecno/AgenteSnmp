package collector

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/asaavedra/agent-snmp/pkg/models"
	"github.com/asaavedra/agent-snmp/pkg/profile"
	"github.com/asaavedra/agent-snmp/pkg/snmp"
)

// ─────────────────────────────────────────────────────────────────────────────
// Tipo interno de clasificación
// ─────────────────────────────────────────────────────────────────────────────

// supplyClass es el resultado intermedio de classifySupply.
// Se mantiene separado de SupplyData para que classifySupply no dependa
// de models y pueda testearse de forma aislada.
type supplyClass struct {
	Type  string // "toner", "drum", "fuser_kit", "waste_toner", etc.
	Color string // "black", "cyan", "magenta", "yellow", o "n/a"
}

// ─────────────────────────────────────────────────────────────────────────────
// Función principal exportada
// ─────────────────────────────────────────────────────────────────────────────

// CollectSuppliesEdge recolecta los suministros de la impresora usando los tres
// OID-columna definidos en el DeviceProfile (RFC 3805 prtMarkerSuppliesTable).
//
// Flujo:
//  1. Walk sobre BaseDescOID → nombres de suministros + índices de instancia.
//  2. Walk sobre BaseLevelOID → niveles actuales, indexados por sufijo OID.
//  3. Walk sobre BaseMaxOID   → capacidades máximas, indexados por sufijo OID.
//  4. Para cada descripción, construye un SupplyData con payload dual:
//     - Capa UI: Percentage, Status, IsMeasurable.
//     - Capa ML: RawLevel, RawMax (intocables).
//
// El ctx actúa como kill-switch entre walks: si el agente cancela el job
// (shutdown, timeout de ronda), la función aborta limpiamente.
func CollectSuppliesEdge(
	client *snmp.SNMPClient,
	ctx context.Context,
	prof *profile.DeviceProfile,
) []models.SupplyData {
	cfg := prof.OIDs.Supplies
	snmpCtx := snmp.NewContext()

	// ── Walk 1: Descripciones (fuente de verdad para índices) ──────────────
	if err := ctx.Err(); err != nil {
		return nil
	}
	descResults, err := client.Walk(cfg.BaseDescOID, snmpCtx)
	if err != nil || len(descResults) == 0 {
		return nil
	}

	// ── Walk 2: Niveles actuales ───────────────────────────────────────────
	if err := ctx.Err(); err != nil {
		return nil
	}
	levelMap := make(map[string]int64, len(descResults))
	if lvlResults, err := client.Walk(cfg.BaseLevelOID, snmpCtx); err == nil {
		for _, r := range lvlResults {
			if suffix := extractOIDSuffix(cfg.BaseLevelOID, r.OID); suffix != "" {
				if n, ok := parseRawInt(r.Value); ok {
					levelMap[suffix] = n
				}
			}
		}
	}

	// ── Walk 3: Capacidades máximas ────────────────────────────────────────
	if err := ctx.Err(); err != nil {
		return nil
	}
	maxMap := make(map[string]int64, len(descResults))
	if maxResults, err := client.Walk(cfg.BaseMaxOID, snmpCtx); err == nil {
		for _, r := range maxResults {
			if suffix := extractOIDSuffix(cfg.BaseMaxOID, r.OID); suffix != "" {
				if n, ok := parseRawInt(r.Value); ok {
					maxMap[suffix] = n
				}
			}
		}
	}

	// ── Construcción del payload dual ──────────────────────────────────────
	supplies := make([]models.SupplyData, 0, len(descResults))

	for _, descResult := range descResults {
		desc := strings.TrimSpace(descResult.Value)
		if desc == "" {
			continue
		}

		suffix := extractOIDSuffix(cfg.BaseDescOID, descResult.OID)
		if suffix == "" {
			continue
		}

		// Clasificación semántica (tipo + color) sin acoplar al mapa de IDs
		sc := classifySupply(desc)

		// ID determinista: "{type}_{color}_{suffix}" o "{type}_{suffix}" si no hay color
		var id string
		if sc.Color != "n/a" {
			id = fmt.Sprintf("%s_%s_%s", sc.Type, sc.Color, suffix)
		} else {
			id = fmt.Sprintf("%s_%s", sc.Type, suffix)
		}

		// Raw values — usar -2 (RFC 3805: "not available") si el walk no retornó dato
		rawLevel, hasLevel := levelMap[suffix]
		if !hasLevel {
			rawLevel = -2
		}
		rawMax, hasMax := maxMap[suffix]
		if !hasMax {
			rawMax = -2
		}

		supplies = append(supplies, buildEdgeSupply(id, desc, sc, rawLevel, rawMax))
	}

	return supplies
}

// ─────────────────────────────────────────────────────────────────────────────
// Edge Computing: lógica dual UI + ML
// ─────────────────────────────────────────────────────────────────────────────

// buildEdgeSupply aplica la lógica estricta de cálculo dual a un suministro.
// Los campos RawLevel y RawMax NUNCA se modifican: son la fuente de verdad para ML.
// Los campos Percentage, Status e IsMeasurable son el resultado procesado para la UI.
func buildEdgeSupply(id, desc string, sc supplyClass, rawLevel, rawMax int64) models.SupplyData {
	s := models.SupplyData{
		ID:          id,
		Type:        sc.Type,
		Color:       sc.Color,
		Name:        buildFriendlyName(sc.Type, sc.Color),
		Description: desc,
		RawLevel:    rawLevel,
		RawMax:      rawMax,
	}

	switch {
	case rawMax > 0 && rawLevel >= 0:
		// Caso nominal: ambos valores son válidos, el porcentaje es confiable.
		pct := (float64(rawLevel) / float64(rawMax)) * 100
		pct = math.Min(pct, 100.0)             // cap: algún firmware reporta level > max
		pct = math.Round(pct*10) / 10          // redondear a 1 decimal
		s.IsMeasurable = true
		s.Percentage = pct
		s.Status = edgeStatusFromPct(pct)

	case rawLevel == -3:
		// RFC 3805: -3 = capacityUnknown. El suministro está presente pero el
		// dispositivo no reporta un nivel numérico concreto.
		// Percentage=100 evita que la UI muestre el indicador como "vacío".
		s.IsMeasurable = false
		s.Percentage = 100
		s.Status = models.SupplyStatusOK

	default:
		// -2 (not available), -1 (other), max desconocido, o combinación sin sentido.
		// No hay dato suficiente para computar porcentaje.
		s.IsMeasurable = false
		s.Percentage = 0
		s.Status = models.SupplyStatusUnknown
	}

	return s
}

// edgeStatusFromPct convierte un porcentaje calculado al SupplyStatus correspondiente.
// Umbrales alineados con los de getSupplyStatus en data.go para mantener coherencia
// durante la migración gradual.
func edgeStatusFromPct(pct float64) models.SupplyStatus {
	switch {
	case pct >= 75:
		return models.SupplyStatusOK
	case pct >= 25:
		return models.SupplyStatusLow
	case pct >= 10:
		return models.SupplyStatusCritical
	default:
		return models.SupplyStatusEmpty
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Clasificación semántica: Type + Color
// ─────────────────────────────────────────────────────────────────────────────

// classifySupply analiza la descripción textual de un suministro y retorna
// su tipo semántico y color de forma independiente.
//
// Reglas de diseño:
//   - Solo strings.Contains sobre el string normalizado a minúsculas.
//   - NO usa un mapa { "black toner" → "black_toner" }: ese patrón colapsa tipo y color
//     en una sola cadena y rompe la generación del ID determinista por separado.
//   - El color se determina primero porque es ortogonal al tipo.
//   - Los tipos se evalúan de más específico a más genérico para evitar falsos positivos
//     (ej. "waste toner" debe ser waste_toner, no toner).
func classifySupply(description string) supplyClass {
	d := strings.ToLower(description)

	// ── Paso 1: Color ─────────────────────────────────────────────────────
	// Evaluado antes que el tipo porque el color es independiente del componente.
	color := "n/a"
	switch {
	case anyContains(d, "black", "negro", "negra", "noir", " bk ", "-bk", "k toner"):
		color = "black"
	case anyContains(d, "cyan", "cian", "azul", " c toner", "-c "):
		color = "cyan"
	case anyContains(d, "magenta", " mg ", " m toner", "-m "):
		color = "magenta"
	case anyContains(d, "yellow", "amarillo", "jaune", " y toner", "-y "):
		color = "yellow"
	// Xerox slot notation: (R1)=black, (R2)=cyan, (R3)=magenta, (R4)=yellow
	case strings.Contains(d, "(r1)"):
		color = "black"
	case strings.Contains(d, "(r2)"):
		color = "cyan"
	case strings.Contains(d, "(r3)"):
		color = "magenta"
	case strings.Contains(d, "(r4)"):
		color = "yellow"
	}

	// ── Paso 2: Tipo (precedencia: específico → genérico) ─────────────────
	switch {
	case anyContains(d, "waste", "residuo", "recogida", "restant", "collecteur"):
		// Waste toner / depósito de residuos: nunca tiene color relevante
		return supplyClass{Type: "waste_toner", Color: "n/a"}

	case anyContains(d, "fuser", "fusor", "fixiereinheit", "unidad fusora"):
		return supplyClass{Type: "fuser_kit", Color: "n/a"}

	case anyContains(d, "transfer belt", "transfer roller", "transfer unit",
		"transferencia", "cinta de transfer", "correa de transferencia"):
		return supplyClass{Type: "transfer_roller", Color: "n/a"}

	case anyContains(d, "staple", "grapa", "agrafes", "grapas"):
		return supplyClass{Type: "staples", Color: "n/a"}

	case anyContains(d, "maintenance kit", "kit de mantenimiento", "kit mantenimiento"):
		return supplyClass{Type: "maintenance_kit", Color: "n/a"}

	case anyContains(d, "drum", "tambor", "imaging unit", "photoconductor",
		"unidad de imagen", "opc"):
		// Drums sí tienen color en equipos de color
		return supplyClass{Type: "drum", Color: color}

	case anyContains(d, "ink", "tinta", "inkjet"):
		return supplyClass{Type: "ink", Color: color}

	case anyContains(d, "toner", "cartridge", "cartucho", "tonerkassette"):
		return supplyClass{Type: "toner", Color: color}

	// Rodillos de alimentación / separación (después de transfer_roller para evitar captura prematura)
	case anyContains(d, "roller", "rodillo", "pick roller", "feed roller",
		"retard roller", "separation roller"):
		return supplyClass{Type: "roller", Color: "n/a"}

	// Almohadillas de separación
	case anyContains(d, " pad", "separation pad", "almohadilla"):
		return supplyClass{Type: "pad", Color: "n/a"}

	// Kits de mantenimiento genéricos (sin las palabras exactas ya cubiertas arriba)
	case anyContains(d, " kit", "mantenimiento"):
		return supplyClass{Type: "maintenance_kit", Color: "n/a"}
	}

	// Fallback: suministro genérico sin clasificación precisa
	return supplyClass{Type: "supply", Color: color}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers internos
// ─────────────────────────────────────────────────────────────────────────────

// buildFriendlyName construye un nombre legible para la UI a partir del tipo y color.
// No usa strings.Title (deprecated desde Go 1.18) sino capitalización manual.
// Ejemplos: ("toner","black")→"Toner Black" | ("fuser_kit","n/a")→"Fuser Kit"
func buildFriendlyName(supplyType, color string) string {
	parts := strings.Fields(strings.ReplaceAll(supplyType, "_", " "))
	if color != "n/a" {
		parts = append(parts, color)
	}
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// extractOIDSuffix extrae el sufijo de instancia SNMP eliminando el prefijo baseOID.
//
// gosnmp puede retornar OIDs con o sin punto inicial — ambos casos están cubiertos.
//
// Ejemplo:
//
//	base = "1.3.6.1.2.1.43.11.1.1.6"
//	full = ".1.3.6.1.2.1.43.11.1.1.6.1.2"   →  ".1.2"
//	full = "1.3.6.1.2.1.43.11.1.1.6.1.2"    →  ".1.2"
func extractOIDSuffix(baseOID, fullOID string) string {
	base := strings.TrimPrefix(baseOID, ".")
	full := strings.TrimPrefix(fullOID, ".")
	if strings.HasPrefix(full, base) {
		return full[len(base):]
	}
	return ""
}

// parseRawInt convierte un string SNMP a int64 preservando los valores negativos
// con significado semántico en RFC 3805 (-3 = capacityUnknown, -2 = not available,
// -1 = other).
//
// A diferencia de parseCounter (data.go), que retorna -1 para cualquier negativo,
// esta función los preserva intactos para que buildEdgeSupply pueda distinguirlos.
//
// Retorna (0, false) si el valor no es parseable como entero.
func parseRawInt(val string) (int64, bool) {
	val = strings.TrimSpace(val)
	n, err := strconv.ParseInt(val, 10, 64)
	if err == nil {
		return n, true
	}
	// Fallback para "100.0" y otros floats que algunos firmwares retornan
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return 0, false
	}
	return int64(f), true
}

// anyContains retorna true si s contiene alguno de los substrings dados.
// Centraliza la lógica de classifySupply sin contaminar el scope de la función.
func anyContains(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
