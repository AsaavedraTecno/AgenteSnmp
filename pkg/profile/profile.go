package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// ─────────────────────────────────────────────────────────────────────────────
// Structs YAML
// ─────────────────────────────────────────────────────────────────────────────

// DeviceProfile es la representación tipada de un archivo .yml de perfil de dispositivo.
// Convive con el Profile JSON existente en types.go durante la migración gradual.
type DeviceProfile struct {
	// ProfileID es el identificador único del perfil (ej. "generic", "hp", "xerox").
	ProfileID string `yaml:"profile_id"`
	// Brand es el nombre comercial del fabricante para logs y UI.
	Brand string `yaml:"brand"`
	// Description es una nota informativa sobre el alcance del perfil.
	Description string `yaml:"description,omitempty"`

	// MatchOIDPatterns lista los prefijos de sysObjectID que activan este perfil.
	// El motor usa strings.HasPrefix(sysObjectID, patrón) para el match.
	// Ejemplos: "1.3.6.1.4.1.11"   → captura todos los HP
	//           "1.3.6.1.4.1.253"  → captura todos los Xerox
	// Vacío en default.yml: ese perfil solo se retorna como fallback.
	MatchOIDPatterns []string `yaml:"match_oid_patterns"`

	// OIDs agrupa todos los OIDs del perfil en secciones semánticas.
	OIDs OIDs `yaml:"oids"`
}

// OIDList es un campo que acepta tanto un string como una lista de strings en YAML.
// Permite definir múltiples OIDs de fallback por campo de identificación.
// El colector prueba cada OID en orden y usa el primero que responde con un valor válido.
//
// Compatibilidad hacia atrás: un string simple en el YAML se convierte internamente
// a una lista de un elemento, por lo que los perfiles existentes no necesitan modificarse.
//
// Ejemplo YAML con string simple (compatible):
//
//	model: "1.3.6.1.4.1.11.2.3.9.4.2.1.1.3.3.0"
//
// Ejemplo YAML con lista de fallbacks:
//
//	model:
//	  - "1.3.6.1.4.1.11.2.3.9.4.2.1.1.3.3.0"   # OID firmware ≥ 5.x
//	  - "1.3.6.1.2.1.25.3.2.1.3.1"              # hrDeviceDescr fallback
type OIDList []string

// UnmarshalYAML implementa yaml.Unmarshaler para OIDList.
// Acepta un scalar (string) o una secuencia (lista de strings).
func (o *OIDList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		*o = OIDList{value.Value}
		return nil
	case yaml.SequenceNode:
		var list []string
		if err := value.Decode(&list); err != nil {
			return fmt.Errorf("OIDList: error decodificando secuencia: %w", err)
		}
		*o = OIDList(list)
		return nil
	default:
		return fmt.Errorf("OIDList: tipo YAML no soportado (kind=%v)", value.Kind)
	}
}

// OIDs contiene las secciones de OIDs de un DeviceProfile.
// Cada sección es un mapa nombre-semántico → OID en notación punto.
type OIDs struct {
	// Identification contiene los OIDs de identidad del dispositivo.
	// Cada campo acepta un string simple O una lista de OIDs de fallback (OIDList).
	// Claves estándar: sys_descr, hostname, location, contact, uptime,
	//                  model, model_alt, serial_number.
	Identification map[string]OIDList `yaml:"identification"`

	// Counters contiene los OIDs de contadores de páginas.
	// Claves estándar: total_pages, mono_pages, color_pages,
	//                  scan_pages, copy_pages, fax_pages, engine_cycles.
	Counters map[string]string `yaml:"counters"`

	// Status contiene los OIDs de estado del dispositivo.
	// Claves estándar: device_status, printer_status, error_state.
	Status map[string]string `yaml:"status"`

	// Network contiene los OIDs de red.
	// Claves estándar: mac_address, ip_address.
	Network map[string]string `yaml:"network"`

	// Supplies describe cómo leer la tabla de suministros RFC 3805.
	Supplies SupplyConfiguration `yaml:"supplies"`

	// HardwareAlerts describe cómo leer la tabla de alertas de hardware RFC 3805.
	// Opcional: si DescriptionOID está vacío, la recolección de alertas se omite.
	HardwareAlerts HardwareAlertsConfiguration `yaml:"hardware_alerts"`
}

// HardwareAlertsConfiguration define el OID de la tabla de alertas de hardware RFC 3805.
// El motor hace SNMP WALK sobre DescriptionOID para recuperar descripciones textuales
// de atascos y errores activos (prtAlertDescription, .43.18.1.1.8).
type HardwareAlertsConfiguration struct {
	// DescriptionOID apunta a prtAlertDescription (.43.18.1.1.8).
	// Retorna la descripción textual de cada alerta activa (ej. "Paper Jam in Tray 2").
	DescriptionOID string `yaml:"description_oid"`
}

// SupplyConfiguration mapea los tres OID-columna de la tabla prtMarkerSuppliesTable
// (RFC 3805, base: 1.3.6.1.2.1.43.11.1.1).
//
// El motor de recolección hace SNMP WALK sobre cada campo para obtener
// todas las instancias de suministros. El sufijo numérico del OID resultante
// se usa como índice determinista para construir el SupplyData.ID.
type SupplyConfiguration struct {
	// BaseDescOID apunta a prtMarkerSuppliesDescription (.43.11.1.1.6).
	// Retorna el nombre textual del suministro, ej. "Black Toner Cartridge".
	BaseDescOID string `yaml:"base_desc_oid"`

	// BaseLevelOID apunta a prtMarkerSuppliesLevel (.43.11.1.1.9).
	// Nivel actual del suministro.
	// Valores especiales: -3 = almostEmpty (casi vacío, no medible en %),
	//                     -2 = capacityUnknown (el dispositivo no reporta nivel).
	// Valor positivo: cantidad absoluta en las unidades del fabricante.
	BaseLevelOID string `yaml:"base_level_oid"`

	// BaseMaxOID apunta a prtMarkerSuppliesMaxCapacity (.43.11.1.1.8).
	// Capacidad máxima del suministro en las mismas unidades que BaseLevelOID.
	// -2 indica que el dispositivo no reporta capacidad máxima.
	BaseMaxOID string `yaml:"base_max_oid"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Interface y Concrete Manager
// ─────────────────────────────────────────────────────────────────────────────

// ProfileManager es la interfaz del motor de perfiles YAML.
//
// NOTA DE NOMENCLATURA: Se llama ProfileManager y no Manager para coexistir
// sin conflicto con el Manager struct del archivo manager.go, que gestiona
// los perfiles JSON durante la migración gradual. Una vez completada la
// migración, se puede consolidar y renombrar.
type ProfileManager interface {
	// LoadProfiles lee y cachea todos los archivos .yml del directorio dado.
	// Requiere que exista un "default.yml" o retorna error.
	// Es seguro llamar varias veces: reemplaza el estado completo (hot-reload).
	LoadProfiles(directory string) error

	// FindProfile retorna el perfil cuyo MatchOIDPatterns coincida con sysObjectID
	// mediante strings.HasPrefix. Si ningún perfil coincide, retorna el perfil
	// cargado desde default.yml. Nunca retorna nil si LoadProfiles tuvo éxito.
	FindProfile(sysObjectID string) *DeviceProfile
}

// YAMLManager implementa ProfileManager con almacenamiento en memoria.
// Seguro para uso concurrente.
type YAMLManager struct {
	mu             sync.RWMutex
	profiles       []*DeviceProfile // Perfiles con patrones activos (todos excepto default)
	defaultProfile *DeviceProfile   // Fallback cargado desde default.yml
}

// NewYAMLManager construye el manager y carga los perfiles del directorio dado.
// Retorna error si el directorio no existe o si falta default.yml.
func NewYAMLManager(directory string) (*YAMLManager, error) {
	m := &YAMLManager{}
	if err := m.LoadProfiles(directory); err != nil {
		return nil, err
	}
	return m, nil
}

// LoadProfiles implementa ProfileManager.LoadProfiles.
//
// Orden de procesamiento:
//  1. Lee todos los .yml del directorio (otros formatos se ignoran).
//  2. "default.yml" se almacena aparte como fallback universal.
//  3. El resto se añade al slice de perfiles activos para matching.
//
// Un archivo .yml con error se loguea y se omite; no aborta la carga completa.
func (m *YAMLManager) LoadProfiles(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("yaml_manager: no se pudo leer el directorio %q: %w", directory, err)
	}

	var loaded []*DeviceProfile
	var defaultProf *DeviceProfile

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yml" {
			continue
		}

		path := filepath.Join(directory, entry.Name())
		prof, err := loadYAMLFile(path)
		if err != nil {
			// Error no fatal: un perfil roto no debe impedir que el agente arranque.
			fmt.Printf("[YAML_MANAGER] ⚠️  Omitiendo %s: %v\n", entry.Name(), err)
			continue
		}

		if entry.Name() == "default.yml" {
			defaultProf = prof
			fmt.Printf("[YAML_MANAGER] ✅ Fallback cargado   → default.yml (brand=%s)\n", prof.Brand)
		} else {
			loaded = append(loaded, prof)
			fmt.Printf("[YAML_MANAGER] ✅ Perfil cargado     → %s (brand=%s, patrones=%d)\n",
				entry.Name(), prof.Brand, len(prof.MatchOIDPatterns))
		}
	}

	// default.yml es obligatorio: sin él FindProfile no puede garantizar un retorno seguro.
	if defaultProf == nil {
		return fmt.Errorf("yaml_manager: default.yml no encontrado en %q — es obligatorio como fallback", directory)
	}

	m.mu.Lock()
	m.profiles = loaded
	m.defaultProfile = defaultProf
	m.mu.Unlock()

	fmt.Printf("[YAML_MANAGER] Motor listo: %d perfiles activos + 1 fallback.\n", len(loaded))
	return nil
}

// FindProfile implementa ProfileManager.FindProfile.
//
// Algoritmo de matching (sin regex, solo strings.HasPrefix):
//  1. Itera los perfiles en el orden en que fueron cargados del directorio.
//  2. Para cada perfil, itera su slice MatchOIDPatterns.
//  3. Si strings.HasPrefix(sysObjectID, patrón) == true, retorna ese perfil.
//  4. Si ninguno coincide, retorna defaultProfile (nunca nil post-LoadProfiles).
//
// La ausencia de regex es deliberada: el prefijo de enterprise OID
// (ej. "1.3.6.1.4.1.11") es suficiente para identificar fabricantes
// sin ambigüedad y sin el costo de compilar expresiones regulares.
func (m *YAMLManager) FindProfile(sysObjectID string) *DeviceProfile {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sysObjectID = strings.TrimSpace(sysObjectID)
	sysObjectID = strings.TrimPrefix(sysObjectID, ".")

	for _, prof := range m.profiles {
		for _, pattern := range prof.MatchOIDPatterns {
			if pattern != "" && strings.HasPrefix(sysObjectID, pattern) {
				return prof
			}
		}
	}

	// Ningún perfil coincidió: retornar el fallback universal.
	return m.defaultProfile
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers internos
// ─────────────────────────────────────────────────────────────────────────────

// loadYAMLFile parsea un archivo .yml a *DeviceProfile.
// Retorna error si el archivo no existe, está malformado, o le falta profile_id.
func loadYAMLFile(path string) (*DeviceProfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("error leyendo %q: %w", path, err)
	}

	var prof DeviceProfile
	if err := yaml.Unmarshal(data, &prof); err != nil {
		return nil, fmt.Errorf("YAML inválido en %q: %w", path, err)
	}

	if prof.ProfileID == "" {
		return nil, fmt.Errorf("perfil en %q no tiene profile_id definido", path)
	}

	return &prof, nil
}
