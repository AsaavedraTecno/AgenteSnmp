package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config contiene la configuración global del agente
type Config struct {
	Mode string `yaml:"mode"` // cloud-managed | standalone

	// Uploader (CRÍTICO: Conexión con Laravel)
	Uploader struct {
		Enabled        bool   `yaml:"enabled"`
		CloudURL       string `yaml:"cloud_url"`
		AgentKey       string `yaml:"agent_key"`
		IntervalSecs   int    `yaml:"interval_secs"`
		MaxBackoffSecs int    `yaml:"max_backoff_secs"`
	} `yaml:"uploader"`

	// Sinks (Donde guardar los archivos temporales)
	Sinks struct {
		File struct {
			Enabled bool   `yaml:"enabled"`
			Path    string `yaml:"path"`
		} `yaml:"file"`
		// Mantenemos HTTP por compatibilidad
		HTTP struct {
			Enabled           bool   `yaml:"enabled"`
			Endpoint          string `yaml:"endpoint"`
			Retries           int    `yaml:"retries"`
			BackoffMaxSeconds int    `yaml:"backoff_max_seconds"`
		} `yaml:"http"`
	} `yaml:"sinks"`

	// --- FALLBACKS (Valores por defecto si falla la API) ---

	SNMP struct {
		Community string `yaml:"community"`
		Version   string `yaml:"version"`
		Port      uint16 `yaml:"port"`
		TimeoutMs int    `yaml:"timeout_ms"`
		Retries   int    `yaml:"retries"`
	} `yaml:"snmp"`

	Discovery struct {
		Enabled       bool   `yaml:"enabled"`
		IPRange       string `yaml:"ip_range"`
		MaxConcurrent int    `yaml:"max_concurrent"`
	} `yaml:"discovery"`

	Collector struct {
		Enabled bool `yaml:"enabled"`
		DelayMs int  `yaml:"delay_ms"`
	} `yaml:"collector"`

	Logging struct {
		Verbose bool   `yaml:"verbose"`
		Level   string `yaml:"level"`
	} `yaml:"logging"`
}

// LoadConfig carga la configuración desde config.yaml
func LoadConfig(filePath string) (Config, error) {
	var cfg Config

	// Leer archivo
	data, err := os.ReadFile(filePath)
	if err != nil {
		return cfg, fmt.Errorf("error leyendo %s: %w", filePath, err)
	}

	// Parsear YAML
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		return cfg, fmt.Errorf("error parseando YAML: %w", err)
	}

	return cfg, nil
}

// SaveConfig guarda la estructura Config de vuelta al archivo YAML
// Esta función es VITAL para la GUI
func SaveConfig(cfg Config, filePath string) error {
	// Convertir Struct a YAML
	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("error generando YAML: %w", err)
	}

	// Escribir en disco (0644 da permisos de lectura/escritura)
	err = os.WriteFile(filePath, data, 0644)
	if err != nil {
		return fmt.Errorf("error escribiendo archivo config: %w", err)
	}

	return nil
}

// DefaultConfig retorna la configuración por defecto para evitar crashes
func DefaultConfig() Config {
	cfg := Config{
		Mode: "cloud-managed",
	}
	// Defaults locales seguros
	cfg.Uploader.Enabled = true
	cfg.Uploader.IntervalSecs = 30
	cfg.Sinks.File.Enabled = true
	cfg.Sinks.File.Path = "./queue"

	// Defaults SNMP
	cfg.SNMP.Community = "public"
	cfg.SNMP.Version = "2c"
	cfg.SNMP.Port = 161
	cfg.SNMP.TimeoutMs = 2000
	cfg.SNMP.Retries = 1

	cfg.Discovery.MaxConcurrent = 5
	cfg.Collector.Enabled = true
	cfg.Collector.DelayMs = 100

	cfg.Logging.Verbose = true
	cfg.Logging.Level = "info"

	return cfg
}
