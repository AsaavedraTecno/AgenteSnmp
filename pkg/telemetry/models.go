package telemetry

import (
	"time"

	"github.com/asaavedra/agent-snmp/pkg/models"
)

// Telemetry es el payload atómico que representa el estado de UNA impresora.
type Telemetry struct {
	SchemaVersion string          `json:"schema_version"`
	EventID       string          `json:"event_id"`
	CollectedAt   time.Time       `json:"collected_at"`
	Source        AgentSource     `json:"source"`
	Printer       PrinterInfo     `json:"printer"`
	Counters      *CountersOutput `json:"counters"`
	// Supplies usa models.SupplyData directamente: raw_level, raw_max,
	// is_measurable y color viajan intactos al JSON sin conversión intermedia.
	Supplies     []models.SupplyData `json:"supplies,omitempty"`
	Alerts       []AlertInfo         `json:"alerts,omitempty"`
	DeviceAlerts []string            `json:"device_alerts,omitempty"`
	Metrics      *MetricsInfo        `json:"metrics,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Structs de salida jerárquicos para la sección "counters"
// ─────────────────────────────────────────────────────────────────────────────
//
// Estos tipos solo existen para definir el shape del JSON. La lógica interna
// del colector sigue usando NormalizedCounters (mapa plano) y CountersInfo;
// la transformación ocurre en buildCounters() del builder.

// CountersOutput es la raíz del bloque "counters" en el JSON de telemetría.
type CountersOutput struct {
	Absolute      CountersAbsoluteOut      `json:"absolute"`
	LogicalMatrix CountersLogicalMatrixOut `json:"logical_matrix"`
	HardwareUsage CountersHardwareUsageOut `json:"hardware_usage"`
	Confidence    string                   `json:"confidence,omitempty"`
}

// CountersAbsoluteOut contiene los totales brutos de impresión.
type CountersAbsoluteOut struct {
	Total int64 `json:"total"`
	Mono  int64 `json:"mono"`
	Color int64 `json:"color"`
}

// CountersByFunctionOut desglosa por función lógica (imprimir, copiar, fax, informes).
type CountersByFunctionOut struct {
	Print    int64 `json:"print"`
	Copy     int64 `json:"copy"`
	FaxPrint int64 `json:"fax_print"`
	Reports  int64 `json:"reports"`
}

// CountersByModeOut desglosa por modo físico de alimentación.
type CountersByModeOut struct {
	Simplex int64 `json:"simplex"`
	Duplex  int64 `json:"duplex"`
}

// CountersByDestinationOut desglosa escaneos/envíos por destino.
type CountersByDestinationOut struct {
	Email     int64 `json:"email"`
	FTP       int64 `json:"ftp"`
	SMB       int64 `json:"smb"`
	USB       int64 `json:"usb"`
	Others    int64 `json:"others"`
	TotalSend int64 `json:"total_send"`
}

// CountersLogicalMatrixOut agrupa los tres ejes lógicos de análisis.
type CountersLogicalMatrixOut struct {
	ByFunction    CountersByFunctionOut    `json:"by_function"`
	ByMode        CountersByModeOut        `json:"by_mode"`
	ByDestination CountersByDestinationOut `json:"by_destination"`
}

// CountersHardwareUsageOut contiene métricas de desgaste físico del motor.
type CountersHardwareUsageOut struct {
	TotalScans   int64 `json:"total_scans"`
	EngineCycles int64 `json:"engine_cycles"`
}

type AgentSource struct {
	AgentID  string `json:"agent_id"`
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Version  string `json:"version"`
}

type PrinterInfo struct {
	ID              string  `json:"id"`
	IP              string  `json:"ip"`
	Brand           string  `json:"brand"`
	BrandConfidence float64 `json:"brand_confidence"`
	Model           *string `json:"model"`
	SerialNumber    *string `json:"serial_number"`
	Hostname        *string `json:"hostname"`
	MacAddress      *string `json:"mac_address"`
	Location        *string `json:"location"`

	Trays []Tray `json:"trays,omitempty"`
}

// Tray representa el estado de una bandeja de papel en un momento dado.
// Percentage es un puntero para distinguir "0% (vacía)" de "nil (no medible)".
// Los valores de Level -2 y -3 son centinelas RFC 3805:
//   -3 = capacityUnknown (el dispositivo no reporta nivel)
//   -2 = almostEmpty (casi vacío, no medible en porcentaje)
// En ambos casos Percentage será nil en el JSON.
type Tray struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	PaperSize  string `json:"paper_size"`
	Capacity   int64  `json:"capacity"`
	Level      int64  `json:"level"`
	Percentage *int   `json:"percentage"` // null cuando level < 0 (no medible)
}

type StatusInfo struct {
	State               string `json:"state"`
	PageCount           int64  `json:"page_count"`
	SystemUptime        string `json:"system_uptime"`
	SystemUptimeSeconds int64  `json:"system_uptime_seconds"`
	SystemLocation      string `json:"system_location,omitempty"`
}

type AlertInfo struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Severity   string    `json:"severity"`
	Message    string    `json:"message"`
	DetectedAt time.Time `json:"detected_at"`
}

type MetricsInfo struct {
	UptimeSeconds int64           `json:"uptime_seconds,omitempty"`
	Polling       *PollingMetrics `json:"polling,omitempty"`
}

type PollingMetrics struct {
	ResponseTimeMs int       `json:"response_time_ms"`
	PollDurationMs int       `json:"poll_duration_ms"`
	OidSuccessRate float64   `json:"oid_success_rate"`
	RetryCount     int       `json:"retry_count"`
	LastPollAt     time.Time `json:"last_poll_at"`
	NextPollAt     time.Time `json:"next_poll_at"`
	ErrorCount     int       `json:"error_count"`
}
