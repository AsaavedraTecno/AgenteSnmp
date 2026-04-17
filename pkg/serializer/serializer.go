package serializer

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/asaavedra/agent-snmp/pkg/telemetry"
)

// Serializer convierte un Telemetry a JSON bytes
// Responsabilidad ÚNICA: Marshall a JSON
// NO escribe a disco, NO decide destino, NO serializa a otros formatos
type Serializer struct {
	// Configuración futura (ej: incluir campos nil, pretty-print, etc)
}

// NewSerializer crea un nuevo serializador
func NewSerializer() *Serializer {
	return &Serializer{}
}

// Serialize convierte un Telemetry a JSON bytes con formato legible
// Retorna el JSON sin procesar, listo para ser enviado a un Sink
func (s *Serializer) Serialize(t *telemetry.Telemetry) ([]byte, error) {
	if t == nil {
		return nil, fmt.Errorf("telemetry cannot be nil")
	}

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)

	// No escapear HTML para que "&" se vea como "&" y no como "\u0026"
	encoder.SetEscapeHTML(false)

	// Indentación de 2 espacios para legibilidad
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(t); err != nil {
		return nil, fmt.Errorf("failed to serialize telemetry: %w", err)
	}
 	// Encode agrega un newline final, lo removemos
	data := buf.Bytes()
	if len(data) > 0 && data[len(data)-1] == '\n' {
		data = data[:len(data)-1]
	}

	return data, nil
}
