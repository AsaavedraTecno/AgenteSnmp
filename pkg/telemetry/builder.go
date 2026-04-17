package telemetry

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/asaavedra/agent-snmp/pkg/collector"
	"github.com/asaavedra/agent-snmp/pkg/models"
)

type Builder struct {
	source AgentSource
}

func NewBuilder(source AgentSource) *Builder {
	return &Builder{
		source: source,
	}
}

func (b *Builder) sanitizeEmptyString(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func (b *Builder) Build(data *collector.PrinterData, delta *collector.CountersDiff, resetDetected bool) (*Telemetry, error) {
	if data == nil {
		return nil, fmt.Errorf("printer data cannot be nil")
	}

	printer := PrinterInfo{
		ID:              b.buildPrinterID(data),
		IP:              data.IP,
		Brand:           strings.TrimSpace(data.Brand),
		BrandConfidence: data.Confidence,
		Model:           b.sanitizeEmptyString(b.extractModel(data)),
		SerialNumber:    b.sanitizeEmptyString(b.extractSerialNumber(data)),
		Hostname:        b.sanitizeEmptyString(b.extractHostname(data)),
		MacAddress:      b.sanitizeEmptyString(b.ExtractMacAddress(data)),
		Location:        b.sanitizeEmptyString(data.Location),
		Trays:           b.buildTrays(data),
	}

	counters := b.buildCounters(data, delta, resetDetected, data.CounterConfidence)
	supplies := b.buildSupplies(data)
	alerts := b.buildAlerts(data, resetDetected)
	metrics := b.buildMetrics(data)
	eventID := b.buildEventID(printer, data.Timestamp)

	telemetry := &Telemetry{
		SchemaVersion: "1.0.0",
		EventID:       eventID,
		CollectedAt:   data.Timestamp.UTC(),
		Source:        b.source,
		Printer:       printer,
		Counters:      counters,
		Supplies:      supplies,
		Alerts:        alerts,
		DeviceAlerts:  data.DeviceAlerts,
		Metrics:       metrics,
	}

	return telemetry, nil
}

// 👇 FUNCIÓN NUEVA PARA TRANSFORMACIÓN DE BANDEJAS
func (b *Builder) buildTrays(data *collector.PrinterData) []Tray {
	if len(data.Trays) == 0 {
		return nil
	}

	var trays []Tray
	for _, t := range data.Trays {
		// Level < 0 son centinelas RFC 3805 (-3=capacityUnknown, -2=almostEmpty):
		// el nivel no es medible, así que Percentage queda nil en el JSON.
		var pct *int
		if t.Capacity > 0 && t.Level >= 0 {
			v := int((float64(t.Level) / float64(t.Capacity)) * 100)
			pct = &v
		}

		trays = append(trays, Tray{
			Name:       t.Name,
			Status:     t.Status,
			PaperSize:  t.PaperSize,
			Capacity:   t.Capacity,
			Level:      t.Level,
			Percentage: pct,
		})
	}
	return trays
}

func (b *Builder) buildPrinterID(data *collector.PrinterData) string {
	if data.NetworkInfo != nil {
		if macAddress, ok := data.NetworkInfo["macAddress"].(string); ok && macAddress != "" {
			cleanMac := strings.ToLower(strings.ReplaceAll(macAddress, ":", ""))
			if len(cleanMac) >= 12 {
				return cleanMac
			}
		}
	}
	serial := strings.TrimSpace(b.extractSerialNumber(data))
	if serial != "" {
		return strings.ToLower(serial)
	}
	return data.IP
}

func (b *Builder) buildCounters(data *collector.PrinterData, delta *collector.CountersDiff, resetDetected bool, confidence string) *CountersOutput {
	countersToUse := data.NormalizedCounters
	if len(countersToUse) == 0 {
		countersToUse = data.Counters
	}

	if confidence == "" {
		confidence = "unavailable"
	}

	// Siempre retornar un objeto counters aunque esté vacío —
	// el backend Laravel lo espera siempre presente.
	if len(countersToUse) == 0 {
		return &CountersOutput{Confidence: confidence}
	}

	// Atajo para extraer un campo del mapa plano NormalizedCounters.
	ex := func(keys ...string) int64 {
		return int64(b.extractCounter(countersToUse, keys...))
	}

	return &CountersOutput{
		Absolute: CountersAbsoluteOut{
			Total: ex("total_pages"),
			Mono:  ex("mono_pages"),
			Color: ex("color_pages"),
		},
		LogicalMatrix: CountersLogicalMatrixOut{
			ByFunction: CountersByFunctionOut{
				Print:    ex("print_pages"),
				Copy:     ex("copy_pages"),
				FaxPrint: ex("fax_pages"),
				Reports:  0, // sin OID estándar SNMP para informes
			},
			ByMode: CountersByModeOut{
				Simplex: ex("simplex_pages"),
				Duplex:  ex("duplex_pages"),
			},
			ByDestination: CountersByDestinationOut{
				Email:     ex("scan_email"),
				FTP:       ex("scan_ftp"),
				SMB:       ex("scan_smb"),
				USB:       ex("scan_usb"),
				Others:    ex("scan_others"),
				TotalSend: ex("scan_pages"),
			},
		},
		HardwareUsage: CountersHardwareUsageOut{
			TotalScans:   ex("scan_pages"),
			EngineCycles: ex("engine_cycles", "ciclo_motor"),
		},
		Confidence: confidence,
	}
}

func (b *Builder) buildEventID(printer PrinterInfo, timestamp time.Time) string {
	var printerKey string
	if printer.MacAddress != nil && *printer.MacAddress != "" {
		printerKey = *printer.MacAddress
	} else {
		printerKey = printer.IP
	}
	key := strings.ReplaceAll(printerKey, ":", "")
	return fmt.Sprintf("%s::%s::%d", b.source.AgentID, key, timestamp.Unix())
}

// buildSupplies retorna el slice de SupplyData sin ninguna conversión intermedia.
// Antes se remapeaba a SupplyInfo, lo que causaba que raw_level, raw_max,
// is_measurable y color se perdieran en el JSON final.
// Ahora Telemetry.Supplies es []models.SupplyData, por lo que basta con
// devolver el slice tal cual viene de CollectSuppliesEdge.
func (b *Builder) buildSupplies(data *collector.PrinterData) []models.SupplyData {
	if len(data.Supplies) == 0 {
		return nil
	}
	return data.Supplies
}

func (b *Builder) buildAlerts(data *collector.PrinterData, resetDetected bool) []AlertInfo {
	var alerts []AlertInfo

	if resetDetected {
		alerts = append(alerts, AlertInfo{
			ID:         "counter_reset",
			Type:       "system",
			Severity:   "info",
			Message:    "Printer counter reset detected.",
			DetectedAt: data.Timestamp.UTC(),
		})
	}

	for _, sd := range data.Supplies {
		// Solo generar alerta si el estado es accionable y el nivel es medible.
		if !sd.IsMeasurable {
			continue
		}
		if sd.Status != models.SupplyStatusCritical && sd.Status != models.SupplyStatusEmpty {
			continue
		}

		severity := "warning"
		if sd.Status == models.SupplyStatusCritical || sd.Status == models.SupplyStatusEmpty {
			severity = "critical"
		}

		alertID := fmt.Sprintf("alert_%s_%s", sd.ID, strings.ToLower(string(sd.Status)))
		alerts = append(alerts, AlertInfo{
			ID:         alertID,
			Type:       "supply",
			Severity:   severity,
			Message:    fmt.Sprintf("%s is %s (%.1f%%)", sd.Name, sd.Status, sd.Percentage),
			DetectedAt: data.Timestamp.UTC(),
		})
	}

	if len(alerts) == 0 {
		return nil
	}
	return alerts
}

func (b *Builder) buildMetrics(data *collector.PrinterData) *MetricsInfo {
	retryCount := data.ProbeAttempts - 1
	if retryCount < 0 {
		retryCount = 0
	}

	// Calcular oid_success_rate real a partir de los contadores acumulados
	// durante la fase de recolección (identificación + contadores).
	// Si no se acumuló nada (agente sin perfiles), usar 0 en lugar de un valor falso.
	oidSuccessRate := 0.0
	if data.OIDsQueried > 0 {
		oidSuccessRate = float64(data.OIDsResponded) / float64(data.OIDsQueried)
		// Redondear a 2 decimales
		oidSuccessRate = math.Round(oidSuccessRate*100) / 100
	}

	metrics := &MetricsInfo{
		UptimeSeconds: data.UptimeSeconds,
		Polling: &PollingMetrics{
			ResponseTimeMs: int(data.ResponseTime.Milliseconds()),
			PollDurationMs: int(data.ResponseTime.Milliseconds()),
			OidSuccessRate: oidSuccessRate,
			RetryCount:     retryCount,
			LastPollAt:     data.Timestamp.UTC(),
			NextPollAt:     data.Timestamp.UTC().Add(1 * time.Hour),
			ErrorCount:     len(data.Errors),
		},
	}
	return metrics
}

func (b *Builder) extractModel(data *collector.PrinterData) string {
	if data.Identification == nil {
		return ""
	}
	if m, ok := data.Identification["model"].(string); ok && m != "" {
		m = strings.TrimPrefix(m, "Samsung")
		m = strings.TrimSpace(m)
		return m
	}
	if desc, ok := data.Identification["sysDescr"].(string); ok && desc != "" {
		parts := strings.Split(desc, ";")
		if len(parts) > 0 && len(parts[0]) > 2 {
			return strings.TrimSpace(parts[0])
		}
	}
	if data.Brand != "" {
		return data.Brand + " Generic"
	}
	return ""
}

func (b *Builder) extractSerialNumber(data *collector.PrinterData) string {
	if data.Identification == nil {
		return ""
	}
	if s, ok := data.Identification["serial_number"].(string); ok && s != "" {
		if !collector.IsInvalidSerialNumber(s) {
			return s
		}
	}
	if desc, ok := data.Identification["sysDescr"].(string); ok && desc != "" {
		upperDesc := strings.ToUpper(desc)
		for _, tag := range []string{"S/N:", "SN:", "SERIAL:"} {
			if idx := strings.Index(upperDesc, tag); idx != -1 {
				val := desc[idx+len(tag):]
				val = strings.Split(val, ";")[0]
				val = strings.Split(val, ",")[0]
				return strings.TrimSpace(val)
			}
		}
	}
	return ""
}

func (b *Builder) extractHostname(data *collector.PrinterData) string {
	if data.Identification == nil {
		return ""
	}
	if hostname, ok := data.Identification["hostname"].(string); ok && hostname != "" {
		return hostname
	}
	if sysName, ok := data.Identification["sysName"].(string); ok && sysName != "" {
		return sysName
	}
	return ""
}

// ExtractMacAddress busca la MAC del dispositivo en los campos de identificación y red.
// Exportado para que runner.go pueda usarlo en el filtro de lista negra.
func (b *Builder) ExtractMacAddress(data *collector.PrinterData) string {
	// 1. Prioridad al Profile (Si el perfil lo encontró, úsalo)
	if data.Identification != nil {
		if mac, ok := data.Identification["mac_address"].(string); ok && mac != "" && len(mac) > 5 {
			return mac
		}
	}
	// 2. Fallback estándar (NetworkInfo)
	if data.NetworkInfo != nil {
		if mac, ok := data.NetworkInfo["macAddress"].(string); ok && mac != "" {
			return mac
		}
	}
	return ""
}

func (b *Builder) extractCounter(counters map[string]interface{}, keys ...string) int {
	for _, key := range keys {
		if val, ok := counters[key]; ok {
			if intVal, ok := val.(int); ok {
				return intVal
			}
			if int64Val, ok := val.(int64); ok {
				return int(int64Val)
			}
			if floatVal, ok := val.(float64); ok {
				return int(floatVal)
			}
			if strVal, ok := val.(string); ok {
				if intVal, err := strconv.Atoi(strings.TrimSpace(strVal)); err == nil {
					return intVal
				}
			}
		}
	}
	return 0
}

func (b *Builder) cleanSupplyName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if idx := strings.Index(strings.ToUpper(name), ";SN"); idx != -1 {
		name = name[:idx]
		name = strings.TrimSpace(name)
	}
	if idx := strings.Index(strings.ToUpper(name), "S/N:"); idx != -1 {
		name = name[:idx]
		name = strings.TrimSpace(name)
	}
	for _, sep := range []string{"Serial", "Part Number", "PN ", "PN:", "PN=", "P/N:", "P/N ", "Model:", "Version:"} {
		lowerName := strings.ToLower(name)
		lowerSep := strings.ToLower(sep)
		if idx := strings.Index(lowerName, lowerSep); idx != -1 {
			name = name[:idx]
			name = strings.TrimSpace(name)
			break
		}
	}
	name = strings.TrimSuffix(strings.TrimSpace(name), ",")
	name = strings.TrimSpace(name)
	name = strings.Join(strings.Fields(name), " ")
	if len(name) < 3 {
		return ""
	}
	return name
}

func (b *Builder) extractFieldAsString(supply interface{}, keys ...string) string {
	if supplyMap, ok := supply.(map[string]interface{}); ok {
		for _, key := range keys {
			if val, ok := supplyMap[key].(string); ok && val != "" {
				return val
			}
		}
	}
	return ""
}

func (b *Builder) extractFieldAsInt(supply interface{}, keys ...string) int {
	if supplyMap, ok := supply.(map[string]interface{}); ok {
		for _, key := range keys {
			if intVal, ok := supplyMap[key].(int); ok {
				return intVal
			}
			if int64Val, ok := supplyMap[key].(int64); ok {
				return int(int64Val)
			}
			if floatVal, ok := supplyMap[key].(float64); ok {
				return int(floatVal)
			}
			if strVal, ok := supplyMap[key].(string); ok && strVal != "" {
				var intResult int
				if _, err := fmt.Sscanf(strVal, "%d", &intResult); err == nil {
					return intResult
				}
				var floatResult float64
				if _, err := fmt.Sscanf(strVal, "%f", &floatResult); err == nil {
					return int(floatResult)
				}
			}
		}
	}
	return 0
}

func (b *Builder) normalizeToID(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, " ", "_"))
}

func (b *Builder) deduceSupplyType(name string) string {
	lowerName := strings.ToLower(name)
	typeMap := map[string]string{
		"toner": "toner", "drum": "drum", "cilindro": "drum",
		"fuser": "fuser", "fusor": "fuser", "roller": "roller", "rodillo": "roller",
		"cartridge": "cartridge", "cartucho": "cartridge",
		"waste": "waste", "residuo": "waste", "transfer": "transfer",
		"transferencia": "transfer", "pickup": "pickup", "retirada": "pickup",
	}
	for keyword, supplyType := range typeMap {
		if strings.Contains(lowerName, keyword) {
			return supplyType
		}
	}
	return "consumable"
}

func (b *Builder) deduceSupplyStatus(percentage float64) string {
	if percentage < 0 {
		return "ok"
	}
	if percentage <= 10 {
		return "critical"
	}
	if percentage <= 25 {
		return "low"
	}
	if percentage <= 75 {
		return "ok"
	}
	return "good"
}

func (b *Builder) extractSupplyStatus(supply interface{}) string {
	if supplyMap, ok := supply.(map[string]interface{}); ok {
		if status, ok := supplyMap["status"].(string); ok && status != "" && status != "unknown" {
			return status
		}
		percentageInt := b.extractFieldAsInt(supply, "percentage", "percent")
		percentage := float64(percentageInt)
		if percentage == 0 {
			level := int64(b.extractFieldAsInt(supply, "level", "current"))
			maxLevel := int64(b.extractFieldAsInt(supply, "maxLevel", "max"))
			if maxLevel > 0 && level > 0 {
				percentage = (float64(level) * 100) / float64(maxLevel)
			}
		}
		return b.deduceSupplyStatus(percentage)
	}
	return "unknown"
}

func (b *Builder) extractSerialFromDescription(desc string) string {
	descUpper := strings.ToUpper(desc)
	if idx := strings.Index(descUpper, ";SN"); idx != -1 {
		serial := desc[idx+3:]
		serial = strings.TrimSpace(serial)
		serial = strings.TrimSuffix(serial, "unknown")
		if len(strings.TrimSpace(serial)) > 2 {
			return strings.TrimSpace(serial)
		}
	}
	for _, pattern := range []string{"S/N:", "SN:", "Serial:", "serial:"} {
		if idx := strings.Index(descUpper, strings.ToUpper(pattern)); idx != -1 {
			serial := desc[idx+len(pattern):]
			if len(strings.TrimSpace(serial)) > 2 {
				return strings.TrimSpace(serial)
			}
		}
	}
	return ""
}

func (b *Builder) extractPartNumberFromDescription(desc string) string {
	descUpper := strings.ToUpper(desc)
	for _, pattern := range []string{"PN ", "PN:", "P/N:", "P/N ", "PartNumber:", "Part Number:"} {
		if idx := strings.Index(descUpper, strings.ToUpper(pattern)); idx != -1 {
			partNum := desc[idx+len(pattern):]
			partNum = strings.TrimSpace(partNum)
			for _, delim := range []string{";", ",", " S/N", " SN:", "Serial"} {
				if delimIdx := strings.Index(strings.ToUpper(partNum), strings.ToUpper(delim)); delimIdx != -1 {
					partNum = partNum[:delimIdx]
					break
				}
			}
			partNum = strings.TrimSpace(partNum)
			if len(partNum) > 2 && partNum != "unknown" {
				return partNum
			}
		}
	}
	return ""
}
