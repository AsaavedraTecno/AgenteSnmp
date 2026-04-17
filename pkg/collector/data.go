package collector

import (
	"context"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/asaavedra/agent-snmp/pkg/discovery"
	"github.com/asaavedra/agent-snmp/pkg/models"
	"github.com/asaavedra/agent-snmp/pkg/profile"
	"github.com/asaavedra/agent-snmp/pkg/snmp"
)

// --- ESTRUCTURAS DE DATOS ---

type PrinterData struct {
	IP                 string                 `json:"ip"`
	Brand              string                 `json:"brand"`
	Confidence         float64                `json:"confidence"`
	Location           string                 `json:"location"`
	Uptime             string                 `json:"uptime"`
	CounterConfidence  string                 `json:"counterConfidence,omitempty"`
	Identification     map[string]interface{} `json:"identification"`
	Status             map[string]interface{} `json:"status"`
	Supplies           []models.SupplyData    `json:"supplies"`
	Counters           map[string]interface{} `json:"counters"`
	Trays              []TrayInfo             `json:"trays"`
	NetworkInfo        map[string]interface{} `json:"networkInfo,omitempty"`
	AdminInfo          map[string]interface{} `json:"adminInfo,omitempty"`
	NormalizedCounters map[string]interface{} `json:"normalizedCounters,omitempty"`
	UptimeSeconds      int64                  `json:"uptime_seconds,omitempty"`
	DeviceAlerts       []string               `json:"device_alerts,omitempty"`
	Errors             []string               `json:"errors"`
	MissingSections    []string               `json:"missingSections"`
	Timestamp          time.Time              `json:"timestamp"`
	ResponseTime       time.Duration          `json:"responseTime"`
	ProbeAttempts      int                    `json:"probeAttempts"`
	// OIDsQueried y OIDsResponded se acumulan durante la recolección para calcular
	// oid_success_rate real en lugar de usar el valor hardcodeado 0.95.
	OIDsQueried   int `json:"oids_queried,omitempty"`
	OIDsResponded int `json:"oids_responded,omitempty"`
}

type TrayInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	PaperSize   string `json:"paper_size"`
	Capacity    int64  `json:"capacity"`
	Level       int64  `json:"level"`
	Description string `json:"description"`
}

type CountersInfo struct {
	TotalPages   int64 `json:"total_pages"`
	MonoPages    int64 `json:"mono_pages"`
	ColorPages   int64 `json:"color_pages"`
	ScanPages    int64 `json:"scan_pages"`
	CopyPages    int64 `json:"copy_pages"`
	FaxPages     int64 `json:"fax_pages"`
	DuplexPages  int64 `json:"duplex_pages,omitempty"`
	SimplexPages int64 `json:"simplex_pages,omitempty"`
	DuplexMono   int64 `json:"duplex_mono,omitempty"`
	DuplexColor  int64 `json:"duplex_color,omitempty"`
	EngineCycles int64 `json:"engine_cycles"`
	EngineMono   int64 `json:"engine_mono,omitempty"`
	EngineColor  int64 `json:"engine_color,omitempty"`
	// Páginas por bandeja de origen (Samsung: OIDs .33.0 y .31.0)
	Tray1Pages int64 `json:"tray1_pages,omitempty"`
	MpPages    int64 `json:"mp_pages,omitempty"`
}

type CountersDiff struct {
	TotalPages   int64 `json:"total_pages"`
	MonoPages    int64 `json:"mono_pages"`
	ColorPages   int64 `json:"color_pages"`
	ScanPages    int64 `json:"scan_pages"`
	CopyPages    int64 `json:"copy_pages"`
	FaxPages     int64 `json:"fax_pages"`
	DuplexPages  int64 `json:"duplex_pages,omitempty"`
	SimplexPages int64 `json:"simplex_pages,omitempty"`
	DuplexMono   int64 `json:"duplex_mono,omitempty"`
	DuplexColor  int64 `json:"duplex_color,omitempty"`
	EngineCycles int64 `json:"engine_cycles"`
	EngineMono   int64 `json:"engine_mono,omitempty"`
	EngineColor  int64 `json:"engine_color,omitempty"`
	Tray1Pages   int64 `json:"tray1_pages,omitempty"`
	MpPages      int64 `json:"mp_pages,omitempty"`
}

type CountersSnapshot struct {
	Absolute      CountersInfo  `json:"absolute"`
	Delta         *CountersDiff `json:"delta"`
	ResetDetected bool          `json:"reset_detected,omitempty"`
	Confidence    string        `json:"confidence,omitempty"`
}

type PrinterState struct {
	LastPollAt time.Time    `json:"last_poll_at"`
	Counters   CountersInfo `json:"counters"`
}

type DeviceInfo struct {
	IP              string
	Brand           string
	BrandConfidence float64
	SysDescr        string
	SysObjectID     string // Recibido del scanner; evita un round-trip SNMP extra en el colector
	Community       string
	SNMPVersion     string
}

type Config struct {
	Timeout                  time.Duration
	Retries                  int
	MaxConcurrentConnections int
	MaxOidsPerDevice         int
	MinDelayBetweenQueries   time.Duration
	Community                string
	SNMPVersion              string
	SNMPPort                 uint16
}

type DataCollector struct {
	config       Config
	rateLimiter  *RateLimiter
	yamlProfiles profile.ProfileManager // Único gestor de perfiles (YAML)
}

// --- CONSTRUCTOR ---

func NewDataCollector(config Config) *DataCollector {
	return &DataCollector{
		config:      config,
		rateLimiter: NewRateLimiter(config.MaxConcurrentConnections),
	}
}

// WithYAMLProfiles inyecta el gestor de perfiles YAML inicializado a nivel de aplicación.
// Debe llamarse una sola vez tras NewDataCollector, antes de iniciar ciclos de recolección.
// El ProfileManager NO se instancia aquí para evitar I/O de disco en cada ciclo.
func (dc *DataCollector) WithYAMLProfiles(pm profile.ProfileManager) *DataCollector {
	dc.yamlProfiles = pm
	return dc
}

// --- CORE: LÓGICA DE RECOLECCIÓN ---

func (dc *DataCollector) CollectData(ctx context.Context, devices []DeviceInfo) ([]PrinterData, error) {
	results := make([]PrinterData, 0, len(devices))
	resultsChan := make(chan PrinterData, len(devices))
	var wg sync.WaitGroup

	// Limitar goroutines concurrentes para evitar OOM en redes grandes.
	// Usamos MaxConcurrentConnections como techo; mínimo 1.
	maxWorkers := dc.config.MaxConcurrentConnections
	if maxWorkers <= 0 {
		maxWorkers = 10
	}
	sem := make(chan struct{}, maxWorkers)

	log.Printf("Iniciando recolección de %d dispositivos (workers=%d)...", len(devices), maxWorkers)
	startTime := time.Now()

	for _, device := range devices {
		wg.Add(1)
		sem <- struct{}{} // adquirir slot — bloquea si el pool está lleno
		go func(devInfo DeviceInfo) {
			defer wg.Done()
			defer func() { <-sem }() // liberar slot al terminar
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[%s] panic recuperado en goroutine de recolección: %v", devInfo.IP, r)
					empty := dc.initializePrinterData(devInfo)
					empty.Errors = append(empty.Errors, fmt.Sprintf("panic: %v", r))
					resultsChan <- empty
				}
			}()
			data := dc.collectFromDevice(ctx, devInfo)
			resultsChan <- data
		}(device)
	}

	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	for data := range resultsChan {
		results = append(results, data)
	}

	log.Printf("Recolección completada en %.2fs.", time.Since(startTime).Seconds())
	return results, nil
}

func (dc *DataCollector) collectFromDevice(ctx context.Context, devInfo DeviceInfo) PrinterData {
	startTime := time.Now()

	// Usar la versión SNMP detectada durante el discovery (v2c o v1).
	// Mínimo 3 segundos de timeout para impresoras lentas (Samsung, Kyocera).
	snmpVersion := devInfo.SNMPVersion
	if snmpVersion == "" {
		snmpVersion = "2c"
	}
	timeout := dc.config.Timeout
	if timeout < 3*time.Second {
		timeout = 3 * time.Second
	}
	retries := dc.config.Retries
	if retries < 2 {
		retries = 2
	}
	client := snmp.NewSNMPClient(devInfo.IP, dc.config.SNMPPort, devInfo.Community, snmpVersion, timeout, retries)

	data := dc.initializePrinterData(devInfo)

	// FASE 1: Identificación base (OIDs estándar RFC 3805 siempre disponibles)
	dc.collectIdentification(&data, client)

	// FASE 0: Selección de perfil YAML vía sysObjectID.
	// El sysObjectID ya llegó del scanner en devInfo.SysObjectID; solo ejecutamos
	// live discovery si no vino (degradación segura: FindProfile devuelve default.yml).
	var devProfile *profile.DeviceProfile
	if dc.yamlProfiles != nil {
		sysOID := devInfo.SysObjectID
		if sysOID == "" {
			disc := discovery.NewDiscoverer(
				devInfo.IP, devInfo.Community, devInfo.SNMPVersion, dc.config.SNMPPort,
			)
			if identity, err := disc.Identify(ctx); err == nil {
				sysOID = identity.SysObjectID
			}
			// Si Identify falla (timeout, no-SNMP): sysOID queda "" →
			// FindProfile retorna default.yml → CollectSuppliesEdge usa RFC 3805 genérico.
		}
		devProfile = dc.yamlProfiles.FindProfile(sysOID)
		profileID := "nil"
		if devProfile != nil {
			profileID = devProfile.ProfileID
		}
		log.Printf("[%s][PROFILE_SEL] sysObjectID=%q → perfil=%s\n", devInfo.IP, sysOID, profileID)
	}

	// FASE 1b: Identificación específica del fabricante (OIDs propietarios del perfil YAML)
	dc.collectIdentificationFromYAML(&data, client, devProfile)

	// FASE 1c: Recolectar uptime (RFC 1213 universal, con override por perfil)
	dc.collectUptimeFromYAML(&data, client, devProfile)

	// FASE 2: Contadores desde perfil YAML (o fallback estándar RFC 3805)
	dc.collectCountersFromYAML(&data, client, devProfile)

	// FASE 3: Suministros — recolector edge tipado
	// devProfile nunca es nil si yamlProfiles fue inicializado (FindProfile garantiza fallback).
	if devProfile != nil {
		data.Supplies = CollectSuppliesEdge(client, ctx, devProfile)
	}
	// Si yamlProfiles es nil (agente sin perfiles YAML), Supplies queda vacío []
	// y el pipeline continúa normalmente (degradación segura).

	dc.collectInputTrays(&data, client)
	dc.collectStatus(&data, client)
	dc.collectNetworkInfo(&data, client)
	dc.collectHardwareAlerts(&data, client, ctx, devProfile)

	// FASE 4: Consolidación de contadores
	dc.consolidateCounters(&data)
	dc.normalizeData(&data)
	data.ResponseTime = time.Since(startTime)
	dc.recordMissingSections(&data)

	return data
}

// --- MÓDULO DE IDENTIFICACIÓN ---

// collectIdentification consulta los OIDs estándar RFC 3805 para identificar el dispositivo.
// Los OIDs de identificación específicos del fabricante (ej. HP device-id) se agregan
// desde el perfil YAML en una segunda pasada si el perfil ya fue determinado.
func (dc *DataCollector) collectIdentification(data *PrinterData, client *snmp.SNMPClient) {
	oidsToQuery := []string{
		"1.3.6.1.2.1.1.1.0",         // sysDescr
		"1.3.6.1.2.1.1.5.0",         // sysName
		"1.3.6.1.2.1.1.6.0",         // sysLocation
		"1.3.6.1.2.1.25.3.2.1.3.1",  // hrDeviceDescr
		"1.3.6.1.2.1.43.5.1.1.5.1",  // RFC Model
		"1.3.6.1.2.1.43.5.1.1.17.1", // RFC Serial
	}

	oidToField := map[string]string{
		"1.3.6.1.2.1.1.1.0":         "sysDescr",
		"1.3.6.1.2.1.1.5.0":         "hostname",
		"1.3.6.1.2.1.1.6.0":         "location",
		"1.3.6.1.2.1.25.3.2.1.3.1":  "model_hr",
		"1.3.6.1.2.1.43.5.1.1.17.1": "serial_rfc",
		"1.3.6.1.2.1.43.5.1.1.5.1":  "model_rfc",
	}

	ctx := snmp.NewContext()
	results, err := client.GetMultiple(oidsToQuery, ctx)
	if err != nil {
		return
	}

	for oid, val := range results {
		if val == nil {
			continue
		}
		valStr := strings.TrimSpace(fmt.Sprintf("%v", val))
		if valStr == "" || valStr == "0" {
			continue
		}

		cleanOID := strings.TrimPrefix(oid, ".")

		if fieldName, ok := oidToField[cleanOID]; ok {
			switch fieldName {
			case "model":
				data.Identification["model"] = valStr
			case "serial_number":
				data.Identification["serial_number"] = valStr
			case "model_hr", "model_rfc":
				if _, exists := data.Identification["model"]; !exists {
					data.Identification["model"] = valStr
				}
			case "serial_rfc":
				if _, exists := data.Identification["serial_number"]; !exists {
					data.Identification["serial_number"] = valStr
				}
			case "sysLocation", "location":
				if IsValidLocation(valStr) {
					data.Location = valStr
				}
			default:
				data.Identification[fieldName] = valStr
			}
		}
	}
}

// collectIdentificationFromYAML agrega los OIDs de identificación específicos del perfil YAML.
// Cada campo en identification puede definir un string simple O una lista de OIDs de fallback.
// El colector hace un GET masivo de todos los OIDs en un solo paquete y luego, por cada
// campo, prueba los OIDs en orden hasta encontrar el primero con un valor válido.
func (dc *DataCollector) collectIdentificationFromYAML(data *PrinterData, client *snmp.SNMPClient, prof *profile.DeviceProfile) {
	if prof == nil || len(prof.OIDs.Identification) == 0 {
		return
	}

	// Recopilar todos los OIDs únicos de todos los campos para GET masivo.
	oidSeen := make(map[string]bool)
	var allOIDs []string
	for _, oidList := range prof.OIDs.Identification {
		for _, oid := range oidList {
			clean := strings.TrimPrefix(oid, ".")
			if clean != "" && !oidSeen[clean] {
				allOIDs = append(allOIDs, clean)
				oidSeen[clean] = true
			}
		}
	}

	if len(allOIDs) == 0 {
		return
	}

	ctx := snmp.NewContext()
	results, err := client.GetMultiple(allOIDs, ctx)
	if err != nil {
		return
	}

	// Acumular para oid_success_rate real
	data.OIDsQueried += len(allOIDs)
	data.OIDsResponded += len(results)

	// Por cada campo, probar OIDs en orden y usar el primero con valor válido.
	for field, oidList := range prof.OIDs.Identification {
		for _, oid := range oidList {
			clean := strings.TrimPrefix(oid, ".")
			val, ok := results[clean]
			if !ok || val == nil {
				continue
			}
			valStr := strings.TrimSpace(fmt.Sprintf("%v", val))
			if valStr == "" || valStr == "0" {
				continue
			}

			// OID especial HP: string MDL:model;SN:serial...
			if clean == "1.3.6.1.4.1.11.2.3.9.1.1.7.0" {
				dc.parseHPIdentificationString(valStr, data)
				break
			}

			switch field {
			case "model":
				data.Identification["model"] = valStr
			case "serial_number":
				data.Identification["serial_number"] = valStr
			case "location":
				if IsValidLocation(valStr) {
					data.Location = valStr
				}
			default:
				data.Identification[field] = valStr
			}
			break // primer OID válido encontrado — no seguir probando para este campo
		}
	}
}

func (dc *DataCollector) parseHPIdentificationString(idString string, data *PrinterData) {
	pairs := strings.Split(idString, ";")
	for _, pair := range pairs {
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if val == "" {
			continue
		}

		switch key {
		case "MDL":
			data.Identification["model"] = val
		case "SN":
			data.Identification["serial_number"] = val
		}
	}
}

// collectUptimeFromYAML recolecta el uptime del dispositivo.
// Usa el OID definido en identification.uptime del perfil si existe;
// de lo contrario usa 1.3.6.1.2.1.1.3.0 (sysUpTime, RFC 1213 — universal).
// Los TimeTicks SNMP vienen en centésimas de segundo: se dividen entre 100 para obtener segundos.
func (dc *DataCollector) collectUptimeFromYAML(data *PrinterData, client *snmp.SNMPClient, prof *profile.DeviceProfile) {
	const rfcUptimeOID = "1.3.6.1.2.1.1.3.0"

	// Determinar el OID a usar: primer OID de la lista del perfil, si existe.
	uptimeOID := rfcUptimeOID
	if prof != nil {
		if oidList, ok := prof.OIDs.Identification["uptime"]; ok && len(oidList) > 0 && oidList[0] != "" {
			uptimeOID = strings.TrimPrefix(oidList[0], ".")
		}
	}

	// Si collectIdentificationFromYAML ya lo populó, leerlo desde ahí.
	if raw, ok := data.Identification["uptime"].(string); ok && raw != "" && raw != "0" {
		if ticks, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil {
			data.UptimeSeconds = ticks / 100
			return
		}
	}

	// No estaba en la fase de identificación (perfil de marca sin uptime): GET directo.
	snmpCtx := snmp.NewContext()
	val, err := client.Get(uptimeOID, snmpCtx)
	if err != nil {
		return
	}
	raw := strings.TrimSpace(fmt.Sprintf("%v", val))
	if raw == "" || raw == "0" {
		return
	}
	ticks, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return
	}
	data.UptimeSeconds = ticks / 100
}

// collectHardwareAlerts hace SNMP WALK sobre el OID de alertas de hardware.
// Usa hardware_alerts.description_oid del perfil si está definido;
// de lo contrario usa 1.3.6.1.2.1.43.18.1.1.8 (prtAlertDescription, RFC 3805 — universal).
// Decodifica hex que algunos firmwares usan para strings con caracteres especiales.
func (dc *DataCollector) collectHardwareAlerts(data *PrinterData, client *snmp.SNMPClient, ctx context.Context, prof *profile.DeviceProfile) {
	const rfcAlertOID = "1.3.6.1.2.1.43.18.1.1.8"

	alertOID := rfcAlertOID
	if prof != nil && prof.OIDs.HardwareAlerts.DescriptionOID != "" {
		alertOID = strings.TrimPrefix(prof.OIDs.HardwareAlerts.DescriptionOID, ".")
	}

	snmpCtx := snmp.NewContext()
	results, err := client.Walk(alertOID, snmpCtx)
	if err != nil || len(results) == 0 {
		return
	}

	decoder := snmp.HexDecoder{}
	for _, r := range results {
		if r.Value == "" {
			continue
		}
		decoded := strings.TrimSpace(decoder.DecodeValue(r.Value))
		if decoded != "" {
			data.DeviceAlerts = append(data.DeviceAlerts, decoded)
		}
	}
}

// --- MÓDULO DE CONTADORES ---

// collectCountersFromYAML lee los contadores de páginas usando los OIDs definidos en el
// perfil YAML (devProfile.OIDs.Counters). Si el mapa de contadores está vacío o el perfil
// es nil, recurre al fallback RFC 3805 estándar.
//
// El mapa counters del YAML es: nombre_semántico → OID (ej: "total_pages" → "1.3.6.1...")
// Un mismo OID puede tener varios nombres semánticos (ej: total_pages y engine_cycles
// apuntan al mismo OID en Xerox); el colector los resuelve iterando el mapa completo.
func (dc *DataCollector) collectCountersFromYAML(data *PrinterData, client *snmp.SNMPClient, prof *profile.DeviceProfile) {
	if prof == nil || len(prof.OIDs.Counters) == 0 {
		dc.collectCountersStandard(data, client)
		return
	}

	// Construir slice de OIDs único y mapa inverso OID → []nombre
	oidSeen := make(map[string]bool)
	var oids []string
	oidToNames := make(map[string][]string)

	for name, oid := range prof.OIDs.Counters {
		clean := strings.TrimPrefix(oid, ".")
		if clean == "" {
			continue
		}
		if !oidSeen[clean] {
			oids = append(oids, clean)
			oidSeen[clean] = true
		}
		oidToNames[clean] = append(oidToNames[clean], name)
	}

	if len(oids) == 0 {
		dc.collectCountersStandard(data, client)
		return
	}

	log.Printf("[%s][COUNTER_YAML] Consultando %d OIDs: %v\n", data.IP, len(oids), oids)

	ctx := snmp.NewContext()
	results, err := client.GetMultiple(oids, ctx)
	if err != nil {
		log.Printf("[%s][COUNTER_YAML] ❌ GetMultiple falló: %v → fallback RFC 3805\n", data.IP, err)
		dc.collectCountersStandard(data, client)
		return
	}

	log.Printf("[%s][COUNTER_YAML] Respuestas recibidas: %d/%d OIDs\n", data.IP, len(results), len(oids))

	// Acumular para oid_success_rate real
	data.OIDsQueried += len(oids)
	data.OIDsResponded += len(results)

	data.CounterConfidence = "profiled"
	for oid, val := range results {
		clean := strings.TrimPrefix(oid, ".")
		rawStr := fmt.Sprintf("%v", val)
		if names, ok := oidToNames[clean]; ok {
			intVal := parseCounter(rawStr)
			if intVal >= 0 && !isSuspiciousValue(intVal) {
				log.Printf("[%s][COUNTER_YAML] ✅ %-40s → %s=%d (nombres: %v)\n", data.IP, clean, rawStr, intVal, names)
				for _, name := range names {
					data.NormalizedCounters[name] = intVal
				}
			} else {
				log.Printf("[%s][COUNTER_YAML] ⚠️  %-40s → raw=%q intVal=%d EXCLUIDO (isSuspicious=%v)\n",
					data.IP, clean, rawStr, intVal, isSuspiciousValue(intVal))
			}
		} else {
			log.Printf("[%s][COUNTER_YAML] 🔵 OID no mapeado: %s = %v\n", data.IP, clean, val)
		}
	}

	// Reportar OIDs del perfil que no tuvieron respuesta
	for _, oid := range oids {
		if _, responded := results[oid]; !responded {
			if _, responded2 := results["."+oid]; !responded2 {
				log.Printf("[%s][COUNTER_YAML] ❌ Sin respuesta para: %s (nombres: %v)\n", data.IP, oid, oidToNames[oid])
			}
		}
	}

	// Fallback RFC 3805: si el perfil no produjo ningún total_pages (todos los OIDs
	// privados devolvieron vacío — ej. Samsung ProXpress con MIB distinta), recurrir
	// al contador universal RFC 3805 prtMarkerLifeCount.
	if getInt(data.NormalizedCounters, "total_pages") == 0 {
		log.Printf("[%s][COUNTER_YAML] ⚠️  total_pages=0 tras perfil → intentando fallback RFC 3805\n", data.IP)
		dc.collectCountersStandard(data, client)
	}

	// Fallback Web: la condición está encapsulada en webFallbackNeeded para
	// que agregar soporte a otras marcas no requiera tocar este bloque.
	if webFallbackNeeded(data) {
		dc.applyWebCountersFallback(data)
	}
}

// collectCountersStandard es el fallback RFC 3805 para dispositivos sin perfil YAML
// o cuando el perfil no produce total_pages (ej. Samsung ProXpress con MIB distinta).
// RFC 3805 no tiene OIDs estándar para desglose mono/color — eso es propietario.
// Solo se consulta prtMarkerLifeCount (total vitalicio); consolidateCounters sintetiza
// mono = total para monocromáticas y deja color = 0 cuando no hay suministros de color.
func (dc *DataCollector) collectCountersStandard(data *PrinterData, client *snmp.SNMPClient) {
	data.CounterConfidence = "standard"

	ctx := snmp.NewContext()
	res, _ := client.GetMultiple([]string{"1.3.6.1.2.1.43.10.2.1.4.1.1"}, ctx)

	if val, ok := res["1.3.6.1.2.1.43.10.2.1.4.1.1"]; ok {
		total := parseCounter(fmt.Sprintf("%v", val))
		if total >= 0 {
			data.NormalizedCounters["total_pages"] = total
		}
	}
}

func (dc *DataCollector) inferCounterSource(data *PrinterData) string {
	if strings.HasPrefix(data.CounterConfidence, "profiled") {
		return "profiled"
	}
	return "standard"
}

func (dc *DataCollector) consolidateCounters(data *PrinterData) {
	source := dc.inferCounterSource(data)

	// 1. Extraer valores base
	total := getInt(data.NormalizedCounters, "total_pages")
	mono := getInt(data.NormalizedCounters, "mono_pages")
	color := getInt(data.NormalizedCounters, "color_pages")

	// 2. Detección de dispositivo de color (por suministros)
	isColorDevice := false
	for _, sd := range data.Supplies {
		if sd.Color == "cyan" || sd.Color == "magenta" || sd.Color == "yellow" {
			isColorDevice = true
			break
		}
	}

	// También marcamos como color si el perfil nos trajo un valor de color real
	if color > 0 {
		isColorDevice = true
	}

	if source == "profiled" {

		// DATOS DE PERFIL: Confiamos en los OIDs del perfil.
		// Solo sintetizamos cuando UN campo está ausente (== 0),
		// NUNCA pisamos un campo que ya tiene valor.

		if isColorDevice {
			// Caso A: Tenemos mono y color pero no total → sintetizar total
			if total == 0 && mono > 0 && color > 0 {
				total = mono + color
			}

			// Caso B: Tenemos total y color pero no mono → sintetizar mono
			// SOLO si mono es realmente 0 (no llegó del perfil)
			if total > 0 && color > 0 && mono == 0 {
				if total >= color {
					mono = total - color
				}
			}

			// Caso C: Tenemos total y mono pero no color → sintetizar color
			if total > 0 && mono > 0 && color == 0 {
				if total >= mono {
					color = total - mono
				}
			}

			// Caso D: Solo total → al menos guardarlo en mono como fallback
			if total > 0 && mono == 0 && color == 0 {
				mono = total
			}

		} else {
			// Monocromática: color siempre 0
			color = 0
			if total > 0 && mono == 0 {
				mono = total
			}
		}

		log.Printf("[%s][PROFILED] Consolidado -> T:%d M:%d C:%d (isColor:%v)\n",
			data.IP, total, mono, color, isColorDevice)

	} else {
		// ----------------------------------------------------------------
		// DATOS STANDARD (RFC): solo tenemos total_pages del fallback RFC.
		// RFC 3805 no define OIDs estándar para desglose mono/color.
		// Si el dispositivo tiene suministros de color (isColorDevice=true),
		// dejamos mono=total como mejor aproximación; si es monocromática,
		// color=0 y mono=total.
		// ----------------------------------------------------------------
		color = 0
		if total > 0 && mono == 0 {
			mono = total
		}

		log.Printf("[%s][STANDARD] Consolidado -> T:%d M:%d C:%d (isColor:%v)\n",
			data.IP, total, mono, color, isColorDevice)
	}

	data.NormalizedCounters["total_pages"] = total
	data.NormalizedCounters["mono_pages"] = mono
	data.NormalizedCounters["color_pages"] = color
	log.Printf("[%s] Consolidación finalizada. Contadores: %v", data.IP, data.NormalizedCounters)
}

// --- MÓDULO DE BANDEJAS ---

func (dc *DataCollector) collectInputTrays(data *PrinterData, client *snmp.SNMPClient) {
	ctx := snmp.NewContext()

	baseDesc := "1.3.6.1.2.1.43.8.2.1.18.1"
	baseUnit := "1.3.6.1.2.1.43.8.2.1.3.1"
	baseMax := "1.3.6.1.2.1.43.8.2.1.9.1"
	baseLvl := "1.3.6.1.2.1.43.8.2.1.10.1"
	baseX := "1.3.6.1.2.1.43.8.2.1.4.1"
	baseY := "1.3.6.1.2.1.43.8.2.1.5.1"
	baseName := "1.3.6.1.2.1.43.8.2.1.12.1"

	resDesc, _ := client.Walk(baseDesc, ctx)
	if len(resDesc) == 0 {
		return
	}

	resUnit, _ := client.Walk(baseUnit, ctx)
	resMax, _ := client.Walk(baseMax, ctx)
	resLvl, _ := client.Walk(baseLvl, ctx)
	resX, _ := client.Walk(baseX, ctx)
	resY, _ := client.Walk(baseY, ctx)
	resName, _ := client.Walk(baseName, ctx)

	mapUnit := mapByIndex(resUnit)
	mapMax := mapByIndex(resMax)
	mapLvl := mapByIndex(resLvl)
	mapX := mapByIndex(resX)
	mapY := mapByIndex(resY)
	mapName := mapByIndex(resName)

	for _, d := range resDesc {
		idx := getLastIndex(d.OID)
		name := fmt.Sprintf("%v", d.Value)

		unitVal := toInt64(mapUnit[idx])
		maxVal := toInt64(mapMax[idx])
		lvlVal := toInt64(mapLvl[idx])
		xVal := toInt64(mapX[idx])
		yVal := toInt64(mapY[idx])
		mediaName := fmt.Sprintf("%v", mapName[idx])

		paperSizeHuman := "Desconocido"
		if mediaName != "" && len(mediaName) > 1 {
			paperSizeHuman = mediaName
		} else {
			wmm := calcMM(unitVal, yVal)
			hmm := calcMM(unitVal, xVal)
			if wmm > 0 && hmm > 0 {
				std := mapPaperSize(wmm, hmm)
				dims := fmt.Sprintf("%.0fx%.0f mm", wmm, hmm)
				if std != "" {
					paperSizeHuman = fmt.Sprintf("%s (%s)", dims, std)
				} else {
					paperSizeHuman = dims
				}
			}
		}

		status := "No disponible"
		if lvlVal == -3 {
			status = "OK"
		} else if lvlVal == -2 || lvlVal == -1 {
			status = "OK"
		} else if lvlVal == 0 {
			status = "Vacía"
		} else if lvlVal > 0 {
			if maxVal > 0 {
				pct := float64(lvlVal) / float64(maxVal)
				if pct < 0.1 {
					status = "Casi vacía"
				} else {
					status = "OK"
				}
			} else {
				status = "OK"
			}
		}

		data.Trays = append(data.Trays, TrayInfo{
			ID:          idx,
			Name:        name,
			Status:      status,
			PaperSize:   paperSizeHuman,
			Capacity:    maxVal,
			Level:       lvlVal,
			Description: name,
		})
	}
}

func mapByIndex(results []snmp.WalkResult) map[string]interface{} {
	m := make(map[string]interface{})
	for _, r := range results {
		m[getLastIndex(r.OID)] = r.Value
	}
	return m
}

func calcMM(unit, val int64) float64 {
	if val <= 0 {
		return 0
	}
	return float64(val) * float64(unit) / 10000.0
}

func mapPaperSize(w, h float64) string {
	match := func(targetW, targetH float64) bool {
		return math.Abs(w-targetW) < 5 && math.Abs(h-targetH) < 5
	}
	if match(210, 297) || match(297, 210) {
		return "A4"
	}
	if match(216, 279) || match(279, 216) {
		return "Letter"
	}
	if match(216, 356) || match(356, 216) {
		return "Legal"
	}
	return ""
}

// --- UTILITIES ---

func (dc *DataCollector) collectStatus(data *PrinterData, client *snmp.SNMPClient) {
	oids := []string{"1.3.6.1.2.1.25.3.2.1.5.1"}
	res, _ := client.GetMultiple(oids, snmp.NewContext())
	if val, ok := res["1.3.6.1.2.1.25.3.2.1.5.1"]; ok {
		valStr := fmt.Sprintf("%v", val)
		switch valStr {
		case "2":
			data.Status["state"] = "idle"
		case "5":
			data.Status["state"] = "offline"
		default:
			data.Status["state"] = "unknown"
		}
	} else {
		data.Status["state"] = "unknown"
	}
}

func (dc *DataCollector) collectNetworkInfo(data *PrinterData, client *snmp.SNMPClient) {
	ctx := snmp.NewContext()

	// En vez de caminar, pedimos las 4 interfaces más comunes en un solo paquete (GetMultiple)
	macOids := []string{
		"1.3.6.1.2.1.2.2.1.6.1", // Usualmente la LAN en Samsung / Loopback en HP
		"1.3.6.1.2.1.2.2.1.6.2", // Usualmente la LAN en HP
		"1.3.6.1.2.1.2.2.1.6.3", // Wi-Fi o secundaria
		"1.3.6.1.2.1.2.2.1.6.4", // JetDirect u otras
	}

	resMac, _ := client.GetMultiple(macOids, ctx)

	// Revisamos en orden cuál tiene una MAC válida
	for _, oid := range macOids {
		if val, ok := resMac[oid]; ok && val != nil {
			macStr := strings.ToLower(fmt.Sprintf("%v", val))

			// Validamos que parezca una MAC real
			if len(macStr) >= 11 && strings.Contains(macStr, ":") && !strings.Contains(macStr, "00:00:00:00") {
				data.NetworkInfo["macAddress"] = macStr
				break // ¡Encontramos la MAC! Dejamos de buscar.
			}
		}
	}

	// 2. Obtener Location (Esto se mantiene igual)
	resLoc, _ := client.GetMultiple([]string{"1.3.6.1.2.1.1.6.0"}, ctx)
	if val, ok := resLoc["1.3.6.1.2.1.1.6.0"]; ok {
		data.NetworkInfo["location"] = fmt.Sprintf("%v", val)
	}
}

func (dc *DataCollector) initializePrinterData(devInfo DeviceInfo) PrinterData {
	return PrinterData{
		IP:                 devInfo.IP,
		Brand:              devInfo.Brand,
		Confidence:         devInfo.BrandConfidence,
		Identification:     make(map[string]interface{}),
		Status:             make(map[string]interface{}),
		Supplies:           []models.SupplyData{},
		Counters:           make(map[string]interface{}),
		Trays:              []TrayInfo{},
		NetworkInfo:        make(map[string]interface{}),
		AdminInfo:          make(map[string]interface{}),
		NormalizedCounters: make(map[string]interface{}),
		Errors:             []string{},
		MissingSections:    []string{},
		Timestamp:          time.Now(),
		ProbeAttempts:      1,
	}
}

func (dc *DataCollector) normalizeData(data *PrinterData) {
	if len(data.NormalizedCounters) == 0 {
		data.NormalizedCounters = dc.normalizeCounters(data.Counters)
	}
}
func (dc *DataCollector) normalizeCounters(counters map[string]interface{}) map[string]interface{} {
	return counters
}

func (dc *DataCollector) recordMissingSections(data *PrinterData) {
	if len(data.Status) == 0 {
		data.MissingSections = append(data.MissingSections, "status")
	}
	if len(data.Supplies) == 0 {
		data.MissingSections = append(data.MissingSections, "supplies")
	}
	if len(data.Counters) == 0 && len(data.NormalizedCounters) == 0 {
		data.MissingSections = append(data.MissingSections, "counters")
	}
}

func parseCounter(val string) int64 {
	val = strings.TrimSpace(val)
	var num int64
	if _, err := fmt.Sscanf(val, "%d", &num); err == nil {
		if num >= 0 {
			return num
		}
	}
	var f float64
	if _, err := fmt.Sscanf(val, "%f", &f); err == nil {
		if f >= 0 {
			return int64(f)
		}
	}
	return -1
}

// isSuspiciousValue descarta valores que superan 10 mil millones de páginas
// (límite físicamente imposible para cualquier impresora conocida).
func isSuspiciousValue(val int64) bool { return val > 10_000_000_000 }

func getLastIndex(oid string) string {
	parts := strings.Split(oid, ".")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}

func getInt(m map[string]interface{}, key string) int64 {
	if v, ok := m[key]; ok {
		if i, ok := v.(int64); ok {
			return i
		}
		if i, ok := v.(int); ok {
			return int64(i)
		}
	}
	return 0
}

func toInt64(val interface{}) int64 {
	if v, ok := val.(int64); ok {
		return v
	}
	if v, ok := val.(int); ok {
		return int64(v)
	}
	str := fmt.Sprintf("%v", val)
	i, _ := strconv.ParseInt(str, 10, 64)
	return i
}

func getSupplyStatus(percentage float64) string {
	if percentage >= 75 {
		return "OK"
	}
	if percentage >= 50 {
		return "Bueno"
	}
	if percentage >= 25 {
		return "Bajo"
	}
	if percentage >= 10 {
		return "Crítico"
	}
	return "Agotado"
}

// knownInvalidSerials contiene valores que algunas impresoras reportan como serial
// cuando no pueden leer el valor real desde la EEPROM/CRUM.
// Ejemplo: Samsung devuelve 268435456 (0x10000000) en modelos M332x cuando el OID
// de serial no está accesible vía SNMP.
// NOTA: no se rechaza todo serial numérico porque Xerox usa seriales como "3712018754".
var knownInvalidSerials = map[string]bool{
	"268435456": true, // Samsung 0x10000000 — placeholder por defecto M332x/M382x/M402x
	"00000000":  true,
	"00000001":  true,
	"12345678":  true,
	"99999999":  true,
	"unknown":   true,
	"none":      true,
}

func IsInvalidSerialNumber(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) < 3 {
		return true
	}
	if strings.ContainsAny(s, "<>") {
		return true
	}
	return knownInvalidSerials[strings.ToLower(s)]
}

func IsValidLocation(s string) bool { return len(strings.TrimSpace(s)) > 0 }
