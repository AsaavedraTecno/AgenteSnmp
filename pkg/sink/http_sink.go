package sink

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HTTPSink envía los JSON serializados a un endpoint HTTP
// Implementa reintentos con backoff exponencial
type HTTPSink struct {
	endpoint    string        // URL del endpoint (ej: https://cloud.example.com/api/v1/telemetry)
	authToken   string        // Bearer token para autenticación
	client      *http.Client  // cliente HTTP con timeout
	maxRetries  int           // máximo de intentos
	initialWait time.Duration // espera inicial entre reintentos
}

// HTTPSinkConfig configura un HTTPSink
type HTTPSinkConfig struct {
	Endpoint    string        // URL del endpoint
	AuthToken   string        // Bearer token (opcional)
	Timeout     time.Duration // timeout HTTP
	MaxRetries  int           // máximo de reintentos (default: 3)
	InitialWait time.Duration // espera inicial en reintentos (default: 1s)
}

// TODO: Activar HTTPSink cuando endpoint cloud esté disponible
// NewHTTPSink crea un nuevo HTTP sink
func NewHTTPSink(config HTTPSinkConfig) *HTTPSink {
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}

	if config.InitialWait == 0 {
		config.InitialWait = 1 * time.Second
	}

	if config.Timeout == 0 {
		config.Timeout = 10 * time.Second
	}

	client := &http.Client{
		Timeout: config.Timeout,
	}

	return &HTTPSink{
		endpoint:    config.Endpoint,
		authToken:   config.AuthToken,
		client:      client,
		maxRetries:  config.MaxRetries,
		initialWait: config.InitialWait,
	}
}

// TODO: Activar HTTPSink cuando endpoint cloud esté disponible
// Write envía el JSON al endpoint con reintentos exponenciales
func (hs *HTTPSink) Write(ctx context.Context, data []byte, printerID string) error {
	if len(data) == 0 {
		return fmt.Errorf("empty data for printer %s", printerID)
	}

	var lastErr error
	waitDuration := hs.initialWait

	for attempt := 0; attempt <= hs.maxRetries; attempt++ {
		// Si no es el primer intento, esperar con backoff exponencial
		if attempt > 0 {
			select {
			case <-time.After(waitDuration):
				// Esperar completó
			case <-ctx.Done():
				// Contexto cancelado
				return &SinkError{
					Sink:      "http",
					Operation: "write",
					Err:       fmt.Errorf("context cancelled after %d retries", attempt),
					PrinterID: printerID,
				}
			}

			// Aumentar espera para siguiente intento (backoff exponencial)
			waitDuration *= 2
			if waitDuration > 60*time.Second {
				waitDuration = 60 * time.Second // cap a 60s
			}
		}

		// Intentar enviar
		err := hs.sendRequest(ctx, data, printerID)
		if err == nil {
			return nil // Éxito
		}

		lastErr = err

		// Si es el último intento, retornar error
		if attempt == hs.maxRetries {
			return &SinkError{
				Sink:      "http",
				Operation: "write",
				Err:       fmt.Errorf("failed after %d attempts: %w", hs.maxRetries+1, lastErr),
				PrinterID: printerID,
			}
		}
	}

	return lastErr
}

// TODO: Activar HTTPSink cuando endpoint cloud esté disponible
// sendRequest intenta enviar una solicitud HTTP POST
func (hs *HTTPSink) sendRequest(ctx context.Context, data []byte, printerID string) error {
	body := bytes.NewReader(data)

	req, err := http.NewRequestWithContext(ctx, "POST", hs.endpoint, body)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Headers estándar
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Printer-ID", printerID)

	// Autenticación si está configurada
	if hs.authToken != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", hs.authToken))
	}

	// Enviar solicitud
	resp, err := hs.client.Do(req)
	if err != nil {
		return fmt.Errorf("http request failed: %w", err)
	}

	defer resp.Body.Close()

	// Validar status code (2xx = éxito, 4xx = no reintentar, 5xx = reintentar)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil // Éxito
	}

	// Leer body para debugging
	bodyBytes, _ := io.ReadAll(resp.Body)
	bodyStr := string(bodyBytes)

	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		// Error de cliente (400-499) → no reintentar
		return &SinkError{
			Sink:      "http",
			Operation: "write",
			Err:       fmt.Errorf("client error (HTTP %d): %s", resp.StatusCode, bodyStr),
			PrinterID: printerID,
		}
	}

	// Error de servidor (500+) → reintentar
	return fmt.Errorf("server error (HTTP %d): %s", resp.StatusCode, bodyStr)
}

// TODO: Activar HTTPSink cuando endpoint cloud esté disponible
// Close cierra el HTTPSink (no hay recursos especiales)
func (hs *HTTPSink) Close() error {
	// El http.Client no necesita ser cerrado explícitamente
	return nil
}
