package runner

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/asaavedra/agent-snmp/pkg/collector"
	"github.com/asaavedra/agent-snmp/pkg/detector"
	"github.com/asaavedra/agent-snmp/pkg/profile"
	"github.com/asaavedra/agent-snmp/pkg/scanner"
	"github.com/asaavedra/agent-snmp/pkg/serializer"
	"github.com/asaavedra/agent-snmp/pkg/sink"
	"github.com/asaavedra/agent-snmp/pkg/telemetry"
)

// JobParams define qué debe hacer el runner en esta ejecución
type JobParams struct {
	IPRanges      []scanner.IPRangeConfig
	Community     string
	SNMPVersion   string
	SNMPPort      uint16
	Timeout       time.Duration
	Retries       int
	MaxConcurrent int
	DelayBetween  time.Duration
	IgnoredMacs   []string
}

// Runner encapsula las herramientas que persisten entre escaneos
type Runner struct {
	agentSource  telemetry.AgentSource
	builder      *telemetry.Builder
	serializer   *serializer.Serializer
	stateManager *collector.StateManager
	yamlProfiles profile.ProfileManager
	outputDir    string
}

// NewRunner inicializa el runner una sola vez.
// outputDir: directorio de cola (C:\ProgramData\AgentSNMP).
// stateDir:  directorio de estado entre ciclos — separado de la cola para
//            evitar que el uploader procese archivos de estado como telemetría.
func NewRunner(source telemetry.AgentSource, outputDir, stateDir string) *Runner {
	return &Runner{
		agentSource:  source,
		builder:      telemetry.NewBuilder(source),
		serializer:   serializer.NewSerializer(),
		stateManager: collector.NewStateManager(stateDir),
		outputDir:    outputDir,
	}
}

// WithYAMLProfiles inyecta el motor de perfiles YAML en el Runner.
// Llamar antes de iniciar el primer ciclo de escaneo.
func (r *Runner) WithYAMLProfiles(pm profile.ProfileManager) *Runner {
	r.yamlProfiles = pm
	return r
}

// Run ejecuta un ciclo completo de escaneo y guardado
func (r *Runner) Run(ctx context.Context, params JobParams) error {
	log.Printf("🏃 Iniciando trabajo de escaneo: [%d rangos configurados] | Community [%s]", len(params.IPRanges), params.Community)
	startTime := time.Now()

	// 1. Parsear Rango
	ips, err := scanner.GenerateIPsFromRanges(params.IPRanges)
	if err != nil {
		return fmt.Errorf("error parseando rango IP: %v", err)
	}

	// 2. Configurar Discovery
	discConfig := scanner.DiscoveryConfig{
		MaxConcurrentConnections: params.MaxConcurrent,
		TimeoutPerDevice:         params.Timeout,
		Retries:                  params.Retries,
		Community:                params.Community,
		SNMPVersion:              params.SNMPVersion,
		SNMPPort:                 params.SNMPPort,
	}

	// 3. Ejecutar Discovery
	discoveryScanner := scanner.NewDiscoveryScanner(discConfig)
	discoveries, err := discoveryScanner.Scan(ctx, ips)
	if err != nil {
		return fmt.Errorf("error durante discovery: %v", err)
	}

	if len(discoveries) == 0 {
		log.Println("⚠️ No se encontraron dispositivos SNMP en el rango.")
		return nil
	}

	// 4. Procesar Resultados (La lógica que tenías en processPrinters)
	return r.processDiscoveries(ctx, discoveries, params, startTime)
}

func (r *Runner) processDiscoveries(ctx context.Context, discoveries []scanner.DiscoveryResult, params JobParams, startTime time.Time) error {
	// A. Detectar Marcas
	deviceInfos := make([]collector.DeviceInfo, 0, len(discoveries))
	for _, disc := range discoveries {

		brand := detector.DetectBrand(disc.SysDescr)
		confidence := detector.GetBrandConfidence(disc.SysDescr, brand)

		deviceInfos = append(deviceInfos, collector.DeviceInfo{
			IP:              disc.IP,
			Brand:           brand,
			BrandConfidence: confidence,
			SysDescr:        disc.SysDescr,
			SysObjectID:     disc.SysObjectID, // Evita round-trip SNMP extra en el colector
			Community:       params.Community,
			SNMPVersion:     params.SNMPVersion,
		})
	}

	// B. Configurar Colector
	colConfig := collector.Config{
		Timeout:                  params.Timeout,
		Retries:                  params.Retries,
		MaxConcurrentConnections: params.MaxConcurrent,
		MaxOidsPerDevice:         10,
		MinDelayBetweenQueries:   params.DelayBetween,
		Community:                params.Community,
		SNMPVersion:              params.SNMPVersion,
		SNMPPort:                 params.SNMPPort,
	}

	// C. Recolectar Datos
	log.Printf("📊 Recolectando datos de %d dispositivos...", len(deviceInfos))
	dataCollector := collector.NewDataCollector(colConfig)
	if r.yamlProfiles != nil {
		dataCollector.WithYAMLProfiles(r.yamlProfiles)
	}
	printerDataList, err := dataCollector.CollectData(ctx, deviceInfos)
	if err != nil {
		return fmt.Errorf("error recolectando datos: %v", err)
	}

	// D. Preparar Sink (Guardar a archivo)
	fileSink, err := sink.NewFileSink(r.outputDir)
	if err != nil {
		return fmt.Errorf("error iniciando file sink: %v", err)
	}
	defer fileSink.Close()

	bufferedCount := 0

	// E. Procesar y Serializar
	for _, printerData := range printerDataList {

		// 1. Extraemos la MAC usando tu propia función del Builder que ya tienes
		detectedMac := r.builder.ExtractMacAddress(&printerData)

		// 2. Aplicamos la Lista Negra de Laravel
		if isMacIgnored(detectedMac, params.IgnoredMacs) {
			// log.Printf("Ignorando impresora %s por lista negra", detectedMac)
			continue // ¡MAGIA! Cortamos el proceso aquí.
		}

		// Si pasó el filtro, entonces sí hacemos el trabajo pesado:

		// Cálculo de Deltas usando el StateManager del Runner
		var delta *collector.CountersDiff
		var resetDetected bool

		if len(printerData.NormalizedCounters) > 0 || len(printerData.Counters) > 0 {
			countersToUse := printerData.NormalizedCounters
			if len(countersToUse) == 0 {
				countersToUse = printerData.Counters
			}

			currentCounters := collector.CountersInfo{
				TotalPages:   extractCounterInt64(countersToUse, "total_pages"),
				MonoPages:    extractCounterInt64(countersToUse, "mono_pages"),
				ColorPages:   extractCounterInt64(countersToUse, "color_pages"),
				ScanPages:    extractCounterInt64(countersToUse, "scan_pages"),
				CopyPages:    extractCounterInt64(countersToUse, "copy_pages"),
				FaxPages:     extractCounterInt64(countersToUse, "fax_pages"),
				DuplexPages:  extractCounterInt64(countersToUse, "duplex_pages"),
				SimplexPages: extractCounterInt64(countersToUse, "simplex_pages"),
				DuplexMono:   extractCounterInt64(countersToUse, "duplex_mono"),
				DuplexColor:  extractCounterInt64(countersToUse, "duplex_color"),
				EngineCycles: extractCounterInt64(countersToUse, "engine_cycles"),
				EngineMono:   extractCounterInt64(countersToUse, "engine_mono"),
				EngineColor:  extractCounterInt64(countersToUse, "engine_color"),
				Tray1Pages:   extractCounterInt64(countersToUse, "tray1_pages"),
				MpPages:      extractCounterInt64(countersToUse, "mp_pages"),
			}

			delta, resetDetected = r.stateManager.CalculateDelta(printerData.IP, currentCounters)

			if err := r.stateManager.SaveState(printerData.IP, currentCounters); err != nil {
				log.Printf("⚠️ Error guardando estado para %s: %v", printerData.IP, err)
			}
		}

		// Construir Telemetría
		telem, err := r.builder.Build(&printerData, delta, resetDetected)
		if err != nil {
			log.Printf("❌ Error construyendo telemetría %s: %v", printerData.IP, err)
			continue
		}

		// Serializar
		jsonBytes, err := r.serializer.Serialize(telem)
		if err != nil {
			log.Printf("❌ Error serializando %s: %v", printerData.IP, err)
			continue
		}

		// Guardar
		if err := fileSink.Write(ctx, jsonBytes, printerData.IP); err != nil {
			log.Printf("❌ Error escribiendo archivo %s: %v", printerData.IP, err)
			continue
		}
		bufferedCount++
	}

	log.Printf("✅ Ciclo completado en %.2fs. Dispositivos: %d, Archivos en cola: %d",
		time.Since(startTime).Seconds(), len(printerDataList), bufferedCount)

	return nil
}

// Utilidad auxiliar (la misma que tenías en main)
func extractCounterInt64(counters map[string]interface{}, key string) int64 {
	if counters == nil {
		return 0
	}
	val, ok := counters[key]
	if !ok {
		return 0
	}
	switch v := val.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	case string:
		var num int64
		fmt.Sscanf(v, "%d", &num)
		return num
	default:
		return 0
	}
}

// Helper para verificar si una MAC está en la lista negra
func isMacIgnored(mac string, ignoredList []string) bool {
	if mac == "" || len(ignoredList) == 0 {
		return false
	}

	// Normalizamos la MAC detectada (sin dos puntos, minúsculas)
	cleanMac := strings.ToLower(strings.ReplaceAll(mac, ":", ""))

	for _, ignored := range ignoredList {
		cleanIgnored := strings.ToLower(strings.ReplaceAll(ignored, ":", ""))
		if cleanMac == cleanIgnored {
			return true
		}
	}
	return false
}
