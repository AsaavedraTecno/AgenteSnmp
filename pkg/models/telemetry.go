package models

import "time"

// SupplyStatus representa el estado interpretado de un suministro.
// Usar estas constantes evita strings mágicos en el resto del código.
type SupplyStatus string

const (
	SupplyStatusOK       SupplyStatus = "OK"
	SupplyStatusLow      SupplyStatus = "Bajo"
	SupplyStatusCritical SupplyStatus = "Crítico"
	SupplyStatusEmpty    SupplyStatus = "Agotado"
	SupplyStatusUnknown  SupplyStatus = "Desconocido"
)

// SupplyData es el "Edge Payload" dual: contiene datos procesados para la UI
// y los datos crudos intocables para Machine Learning, unidos en una sola estructura.
type SupplyData struct {
	// ID determinista derivado del OID real de la MIB.
	// Formato: "{tipo}_{color}_.{índice_snmp}" → ej. "toner_black_.1.1", "drum_cyan_.1.3"
	// Garantiza unicidad por impresora y permite joins confiables en analytics.
	ID string `json:"id"`

	// --- Metadatos clasificados ---

	// Type clasifica el componente: "toner", "drum", "fuser", "waste_toner", "staples", etc.
	Type string `json:"type"`
	// Color del suministro: "black", "cyan", "magenta", "yellow", "n/a" (para no-tóners).
	Color string `json:"color"`
	// Name es el nombre limpio para mostrar en la UI, ej. "Tóner Negro".
	Name string `json:"name"`
	// Description es la cadena textual original devuelta por la MIB del dispositivo.
	Description string `json:"description"`
	// SerialNumber del cartucho, si el fabricante lo expone vía SNMP.
	SerialNumber string `json:"serial_number,omitempty"`

	// --- Capa Presentación (Edge Computing: UI y disparadores de alertas) ---

	// Percentage es el nivel calculado listo para mostrar (0-100).
	// Sólo es significativo cuando IsMeasurable == true.
	Percentage float64 `json:"percentage"`
	// Status es el estado interpretado para el usuario final.
	// Usar las constantes SupplyStatus* para asignar este campo.
	Status SupplyStatus `json:"status"`
	// IsMeasurable indica si el dispositivo retornó un nivel numérico concreto.
	// false cuando el cartucho reporta sólo "presente/OK" (valores SNMP -3 o -2),
	// lo que hace que Percentage no sea fiable.
	IsMeasurable bool `json:"is_measurable"`

	// --- Capa de Conservación (Machine Learning: datos crudos, nunca modificar) ---

	// RawLevel es el valor exacto devuelto por SNMP.
	// Puede ser -3 (capacityUnknown), -2 (almostEmpty), o un entero absoluto.
	RawLevel int64 `json:"raw_level"`
	// RawMax es la capacidad máxima declarada por el dispositivo vía SNMP.
	// -2 indica que el dispositivo no reporta capacidad máxima.
	RawMax int64 `json:"raw_max"`
}

// PrinterTelemetry es el contenedor final que se envía a Laravel.
// Representa una snapshot completa del estado de la impresora en un instante dado.
type PrinterTelemetry struct {
	IP             string    `json:"ip"`
	SysObjectID    string    `json:"sys_object_id"`
	SysDescr       string    `json:"sys_descr"`
	Uptime         string    `json:"uptime"`
	Timestamp      time.Time `json:"timestamp"`
	ResponseTimeMs int64     `json:"response_time_ms"`

	// Supplies es el arreglo tipado del payload dual.
	// Cada elemento es autónomo: la UI lee Percentage/Status,
	// el pipeline de ML consume RawLevel/RawMax/IsMeasurable.
	Supplies []SupplyData `json:"supplies"`

	// Los siguientes campos mantienen compatibilidad con el resto del flujo actual.
	Identification     map[string]interface{} `json:"identification"`
	Status             map[string]interface{} `json:"status"`
	NormalizedCounters map[string]interface{} `json:"normalized_counters"`
	Trays              []interface{}          `json:"trays"`
	NetworkInfo        map[string]interface{} `json:"network_info"`
	Errors             []string               `json:"errors,omitempty"`
}
