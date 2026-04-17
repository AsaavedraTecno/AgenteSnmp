package remote

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	BaseURL    string
	AgentKey   string
	HTTPClient *http.Client
}

// NewClient crea una nueva instancia del cliente remoto
func NewClient(baseURL, agentKey string) *Client {
	return &Client{
		BaseURL:  baseURL,
		AgentKey: agentKey,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second, // Timeout corto para no bloquear
		},
	}
}

// GetConfig consulta la configuración a la API
func (c *Client) GetConfig() (*RemoteConfig, error) {
	// Construir la URL (Ajusta la ruta según tu ruta en Laravel)
	url := fmt.Sprintf("%s/api/agent/config", c.BaseURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("error creando request: %v", err)
	}

	// Autenticación
	req.Header.Set("X-Agent-Key", c.AgentKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Go-SNMP-Agent/1.0")

	// Ejecutar
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error de conexión: %v", err)
	}
	defer resp.Body.Close()

	// Validar Status Code
	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("error 401: Agent Key inválida o expirada")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("error del servidor: status %d", resp.StatusCode)
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	fmt.Printf("\n📦 RAW JSON DE LARAVEL: %s\n\n", string(bodyBytes))

	// Decodificar JSON desde los bytes
	var config RemoteConfig
	if err := json.Unmarshal(bodyBytes, &config); err != nil {
		return nil, fmt.Errorf("error decodificando JSON: %v", err)
	}

	// Valores por defecto si la API devuelve ceros
	if config.ScanInterval < 30 {
		config.ScanInterval = 300 // Mínimo 5 minutos por seguridad
	}
	if config.MaxConcurrent == 0 {
		config.MaxConcurrent = 10
	}

	return &config, nil
}
