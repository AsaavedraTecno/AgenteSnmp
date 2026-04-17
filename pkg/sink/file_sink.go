package sink

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// FileSink escribe los JSON serializados a archivos en disco
// Usado para buffer/queue cuando la nube no está disponible
type FileSink struct {
	queueDir string
}

// NewFileSink crea un nuevo file sink
// queueDir: directorio donde guardar los archivos (ej: C:\ProgramData\AgentSNMP\queue\)
func NewFileSink(queueDir string) (*FileSink, error) {
	// Crear directorio si no existe
	if err := os.MkdirAll(queueDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create queue directory: %w", err)
	}

	return &FileSink{
		queueDir: queueDir,
	}, nil
}

// Write guarda el JSON en un archivo con naming: {epoch}_{agent_id}_{printer_id}.json
// El archivo queda listo para ser reenviado después
func (fs *FileSink) Write(ctx context.Context, data []byte, printerID string) error {
	if len(data) == 0 {
		return fmt.Errorf("empty data for printer %s", printerID)
	}

	// Generar nombre de archivo: {epoch}_{printer_id}.json
	// El agent_id se agregaría aquí si lo tuviéramos en este contexto
	epoch := time.Now().Unix()
	filename := fmt.Sprintf("%d_%s.json", epoch, printerID)
	filepath := filepath.Join(fs.queueDir, filename)

	// Escribir archivo
	if err := os.WriteFile(filepath, data, 0644); err != nil {
		return &SinkError{
			Sink:      "file",
			Operation: "write", 
			Err:       err,
			PrinterID: printerID,
		}
	}

	return nil
}

// Close cierra el FileSink (no tiene recursos abiertos)
func (fs *FileSink) Close() error {
	// FileSink no mantiene recursos abiertos, así que simplemente retorna nil
	return nil
}
