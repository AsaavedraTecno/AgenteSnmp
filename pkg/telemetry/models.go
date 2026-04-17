package telemetry

import (
	"time"

	"github.com/asaavedra/agent-snmp/pkg/collector"
	"github.com/asaavedra/agent-snmp/pkg/models"
)

// Telemetry es el payload atómico que representa el estado de UNA impresora
type Telemetry struct {
	SchemaVersion string                      `json:"schema_version"`
	EventID       string                      `json:"event_id"`
	CollectedAt   time.Time                   `json:"collected_at"`
	Source        AgentSource                 `json:"source"`
	Printer       PrinterInfo                 `json:"printer"`
	Counters      *collector.CountersSnapshot `json:"counters"`
	// Supplies usa models.SupplyData directamente: así raw_level, raw_max,
	// is_measurable y color viajan intactos al JSON sin ninguna conversión intermedia.
	Supplies      []models.SupplyData         `json:"supplies,omitempty"`
	Alerts        []AlertInfo                 `json:"alerts,omitempty"`
	DeviceAlerts  []string                    `json:"device_alerts,omitempty"`
	Metrics       *MetricsInfo                `json:"metrics,omitempty"`
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

type SupplyInfo struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Type          string  `json:"type"`
	Level         int64   `json:"level"`
	MaxLevel      int64   `json:"max_level"`
	Percentage    float64 `json:"percentage"`
	Status        string  `json:"status"`
	Model         string  `json:"model,omitempty"`
	SerialNumber  string  `json:"serial_number,omitempty"`
	Brand         string  `json:"brand,omitempty"`
	OEM           string  `json:"oem,omitempty"`
	Description   string  `json:"description,omitempty"`
	ComponentType string  `json:"component_type,omitempty"`
	PageCapacity  int64   `json:"page_capacity,omitempty"`
	PartNumber    string  `json:"part_number,omitempty"`
}

type AlertInfo struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Severity   string    `json:"severity"`
	Message    string    `json:"message"`
	DetectedAt time.Time `json:"detected_at"`
}

type CapabilitiesInfo struct {
	SNMPVersion     string   `json:"snmp_version"`
	Duplex          bool     `json:"duplex"`
	Color           bool     `json:"color"`
	Scanner         bool     `json:"scanner"`
	Fax             bool     `json:"fax"`
	OidsSupported   []string `json:"oids_supported"`
	OidsSuccessRate float64  `json:"oids_success_rate"`
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
