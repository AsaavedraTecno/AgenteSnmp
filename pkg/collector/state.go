package collector

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// StateManager maneja la persistencia de estado por impresora
type StateManager struct {
	stateDir string
}

// NewStateManager crea un nuevo gestor de estado
func NewStateManager(stateDir string) *StateManager {
	// Crear directorio si no existe
	os.MkdirAll(stateDir, 0755)
	return &StateManager{stateDir: stateDir}
}

// LoadState carga el estado anterior de una impresora
func (sm *StateManager) LoadState(printerIP string) (*PrinterState, error) {
	filename := sm.getStateFilename(printerIP)

	data, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No existe estado anterior (primer poll)
		}
		return nil, err
	}

	var state PrinterState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}

	return &state, nil
}

// SaveState guarda el estado actual de una impresora (se sobrescribe)
func (sm *StateManager) SaveState(printerIP string, counters CountersInfo) error {
	state := PrinterState{
		LastPollAt: time.Now().UTC(),
		Counters:   counters,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	filename := sm.getStateFilename(printerIP)
	if err := os.WriteFile(filename, data, 0644); err != nil {
		return err
	}

	return nil
}

// CalculateDelta calcula la diferencia entre estado actual y anterior
// Retorna nil si hay reset o no hay estado anterior
// También retorna un booleano indicando si se detectó un reset
func (sm *StateManager) CalculateDelta(printerIP string, currentCounters CountersInfo) (*CountersDiff, bool) {
	previousState, err := sm.LoadState(printerIP)
	if err != nil {
		return nil, false
	}

	// Si no hay estado anterior, no hay delta (primer poll)
	if previousState == nil {
		return nil, false
	}

	// Detectar resets: si actual < anterior en CUALQUIER contador significativo, es un reset.
	// Contadores opcionales (mono, color, scan, copy, fax, engine_cycles) se comparan
	// SOLO si el valor actual es > 0. Si es 0 significa que el perfil actual no recolecta
	// ese OID (ej: engine_cycles eliminado del perfil Samsung), y no debe tratarse como reset.
	// TotalPages siempre se compara porque está en todos los perfiles.
	totalReset := currentCounters.TotalPages < previousState.Counters.TotalPages
	monoReset := currentCounters.MonoPages > 0 && currentCounters.MonoPages < previousState.Counters.MonoPages
	colorReset := currentCounters.ColorPages > 0 && currentCounters.ColorPages < previousState.Counters.ColorPages
	scanReset := currentCounters.ScanPages > 0 && currentCounters.ScanPages < previousState.Counters.ScanPages
	copyReset := currentCounters.CopyPages > 0 && currentCounters.CopyPages < previousState.Counters.CopyPages
	faxReset := currentCounters.FaxPages > 0 && currentCounters.FaxPages < previousState.Counters.FaxPages
	engineReset := currentCounters.EngineCycles > 0 && currentCounters.EngineCycles < previousState.Counters.EngineCycles

	if totalReset || monoReset || colorReset || scanReset || copyReset || faxReset || engineReset {
		return nil, true // delta = nil cuando hay reset, pero reset_detected = true
	}

	// Calcular delta, clampeando a 0 cualquier valor negativo individual.
	// Un delta negativo sin reset global indica estado sucio en el archivo
	// (ej. valor incorrecto guardado por un bug en el ciclo anterior).
	delta := &CountersDiff{
		TotalPages:   maxInt64(0, currentCounters.TotalPages-previousState.Counters.TotalPages),
		MonoPages:    maxInt64(0, currentCounters.MonoPages-previousState.Counters.MonoPages),
		ColorPages:   maxInt64(0, currentCounters.ColorPages-previousState.Counters.ColorPages),
		ScanPages:    maxInt64(0, currentCounters.ScanPages-previousState.Counters.ScanPages),
		CopyPages:    maxInt64(0, currentCounters.CopyPages-previousState.Counters.CopyPages),
		FaxPages:     maxInt64(0, currentCounters.FaxPages-previousState.Counters.FaxPages),
		DuplexPages:  maxInt64(0, currentCounters.DuplexPages-previousState.Counters.DuplexPages),
		SimplexPages: maxInt64(0, currentCounters.SimplexPages-previousState.Counters.SimplexPages),
		DuplexMono:   maxInt64(0, currentCounters.DuplexMono-previousState.Counters.DuplexMono),
		DuplexColor:  maxInt64(0, currentCounters.DuplexColor-previousState.Counters.DuplexColor),
		EngineCycles: maxInt64(0, currentCounters.EngineCycles-previousState.Counters.EngineCycles),
		EngineMono:   maxInt64(0, currentCounters.EngineMono-previousState.Counters.EngineMono),
		EngineColor:  maxInt64(0, currentCounters.EngineColor-previousState.Counters.EngineColor),
		Tray1Pages:   maxInt64(0, currentCounters.Tray1Pages-previousState.Counters.Tray1Pages),
		MpPages:      maxInt64(0, currentCounters.MpPages-previousState.Counters.MpPages),
	}

	return delta, false
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// getStateFilename retorna la ruta del archivo de estado para una impresora
func (sm *StateManager) getStateFilename(printerIP string) string {
	// Sanitizar IP para usarla como filename (reemplazar puntos)
	sanitized := printerIP // puede mejorar si es necesario
	return filepath.Join(sm.stateDir, fmt.Sprintf("printer_%s.json", sanitized))
}
