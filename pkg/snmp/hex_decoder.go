package snmp

import (
	"encoding/hex"
	"strings"
)

// HexDecoder proporciona funciones para decodificar valores hex
type HexDecoder struct{}

// IsHexASCII verifica si un slice de bytes parece ser una cadena hexadecimal ASCII
// Ejemplo: "4150535643" (hex de APSVSC)
func (hd *HexDecoder) IsHexASCII(b []byte) bool {
	if len(b)%2 != 0 {
		return false
	}

	for _, c := range b {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}

	return len(b) > 0
}

// DecodeHexASCII intenta decodificar una cadena hex ASCII a string legible
// Ejemplo: "4150535643" -> "APSVSC"
func (hd *HexDecoder) DecodeHexASCII(b []byte) (string, bool) {
	hexStr := string(b)

	if len(hexStr)%2 != 0 {
		return "", false
	}

	decoded, err := hex.DecodeString(hexStr)
	if err != nil {
		return "", false
	}

	// Verificar si el resultado es texto válido (no caracteres de control)
	if hd.isTextLike(decoded) {
		return string(decoded), true
	}

	return "", false
}

// isTextLike verifica si un slice de bytes parece ser texto legible
func (hd *HexDecoder) isTextLike(b []byte) bool {
	if len(b) == 0 {
		return false
	}

	// Contar caracteres imprimibles
	printableCount := 0
	for _, c := range b {
		if (c >= 32 && c <= 126) || c == '\t' || c == '\n' || c == '\r' {
			printableCount++
		}
	}

	// Si más del 80% son imprimibles, considerar como texto
	return float64(printableCount)/float64(len(b)) > 0.8
}

// DecodeValue intenta decodificar un valor que podría estar en hex
func (hd *HexDecoder) DecodeValue(value interface{}) string {
	switch v := value.(type) {
	case string:
		// Si parece ser hex ASCII, intentar decodificar
		if hd.IsHexASCII([]byte(v)) {
			if decoded, ok := hd.DecodeHexASCII([]byte(v)); ok {
				return decoded
			}
		}
		return v
	case []byte:
		// Si parece ser hex ASCII, intentar decodificar
		if hd.IsHexASCII(v) {
			if decoded, ok := hd.DecodeHexASCII(v); ok {
				return decoded
			}
		}
		// Si parece ser texto, retornar como string
		if hd.isTextLike(v) {
			return strings.TrimRight(string(v), "\x00")
		}
		// Retornar como hex
		return hex.EncodeToString(v)
	default:
		return ""
	}
}

// GetFriendlyHexValue retorna tanto el valor original como el decodificado
func (hd *HexDecoder) GetFriendlyHexValue(value interface{}) (original string, decoded string) {
	var originalStr string

	switch v := value.(type) {
	case string:
		originalStr = v
	case []byte:
		if hd.isTextLike(v) {
			originalStr = string(v)
		} else {
			originalStr = hex.EncodeToString(v)
		}
	}

	decoded = hd.DecodeValue(value)

	return originalStr, decoded
}
