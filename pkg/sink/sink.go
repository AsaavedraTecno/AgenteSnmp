package sink

import (
	"context"
	"fmt"
)

// Sink es la interfaz abstracta para "dónde va el JSON serializado"
// Diferentes implementaciones pueden escribir a:
// - Disco local (buffer/queue)
// - HTTP (cloud)
// - Kafka, database, etc
type Sink interface {
	// Write envía los bytes a su destino
	// Retorna error si no puede escribir
	Write(ctx context.Context, data []byte, printerID string) error

	// Close cierra recursos (conexiones, archivos, etc)
	Close() error
}

// SinkConfig contiene configuración común para todos los sinks
type SinkConfig struct {
	// Configuración compartida (expandir según necesidad)
	Timeout int `yaml:"timeout" json:"timeout"` // segundos
}

// SinkError es un error personalizado que incluye contexto
type SinkError struct {
	Sink      string // nombre del sink (http, file, etc)
	Operation string // operación que falló (write, connect, etc)
	Err       error  // error subyacente
	PrinterID string // ID de la impresora que causó el error
}

// Error implementa la interfaz error
func (se *SinkError) Error() string {
	return fmt.Sprintf("[%s] %s failed for printer %s: %v", se.Sink, se.Operation, se.PrinterID, se.Err)
}

// IsRetryable indica si el error es recuperable (reintentos)
func (se *SinkError) IsRetryable() bool {
	// Los errores de red son recuperables
	// Los errores de validación/auth no
	// Esto se expandirá según necesidad
	return se.Err != nil
}
