package remote

import "github.com/asaavedra/agent-snmp/pkg/scanner"

// RemoteConfig representa la respuesta JSON que envía la Central
type RemoteConfig struct {
	Active        bool                    `json:"active"`         // Si es false, el agente duerme
	IPRanges      []scanner.IPRangeConfig `json:"ip_ranges"`      // Ej: "192.168.150.1 - 192.168.150.150.100  || 192.168.20.1 - 192.168.150.20.100 "
	IPRange       string                  `json:"ip_range"`       // Ej: "192.168.150.1-100"
	Community     string                  `json:"snmp_community"` // Ej: "public"
	Version       string                  `json:"snmp_version"`   // Ej: "2c"
	ScanInterval  int                     `json:"scan_interval"`  // Segundos entre escaneos (ej: 300)
	MaxConcurrent int                     `json:"max_concurrent"` // Hilos simultáneos (opcional)
}
