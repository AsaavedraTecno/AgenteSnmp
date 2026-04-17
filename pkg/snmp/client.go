package snmp

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
)

// SNMPClient wrapper alrededor de gosnmp para manejar SNMP v1/v2c
type SNMPClient struct {
	host      string
	port      uint16
	community string
	version   string
	timeout   time.Duration
	retries   int
}

// NewSNMPClient crea un nuevo cliente SNMP
func NewSNMPClient(host string, port uint16, community, version string, timeout time.Duration, retries int) *SNMPClient {
	return &SNMPClient{
		host:      host,
		port:      port,
		community: community,
		version:   version,
		timeout:   timeout,
		retries:   retries,
	}
}

// Get obtiene un único valor OID
func (sc *SNMPClient) Get(oid string, ctx *Context) (interface{}, error) {
	client, err := sc.connect()
	if err != nil {
		return nil, err
	}
	defer client.Conn.Close()

	result, err := client.Get([]string{oid})
	if err != nil {
		return nil, fmt.Errorf("error SNMP GET: %w", err)
	}

	if result == nil || len(result.Variables) == 0 {
		return nil, fmt.Errorf("sin respuesta para OID: %s", oid)
	}

	variable := result.Variables[0]

	// Verificar si hay error en la respuesta
	if result.Error != gosnmp.NoError {
		return nil, fmt.Errorf("SNMP error %d: %s", result.Error, result.Error.String())
	}

	// Convertir valor a string
	return ParseValue(variable), nil
}

// GetMultiple obtiene múltiples OIDs
func (sc *SNMPClient) GetMultiple(oids []string, ctx *Context) (map[string]interface{}, error) {
	if len(oids) == 0 {
		return make(map[string]interface{}), nil
	}

	client, err := sc.connect()
	if err != nil {
		return nil, err
	}
	defer client.Conn.Close()

	values := make(map[string]interface{})

	// Procesar en batches (Go SNMP tiene límite de 60 OIDs por GET)
	maxOIDsPerBatch := 50 // Usar 50 para ser conservador
	for batchStart := 0; batchStart < len(oids); batchStart += maxOIDsPerBatch {
		batchEnd := batchStart + maxOIDsPerBatch
		if batchEnd > len(oids) {
			batchEnd = len(oids)
		}
		batchOIDs := oids[batchStart:batchEnd]

		result, err := client.Get(batchOIDs)
		if err != nil {
			return nil, fmt.Errorf("error SNMP GET múltiple: %w", err)
		}

		if result == nil {
			return nil, fmt.Errorf("sin respuesta para OIDs")
		}

		for i, variable := range result.Variables {
			if i < len(batchOIDs) {
				parsedValue := ParseValue(variable)
				values[batchOIDs[i]] = parsedValue
			}
		}
	}

	return values, nil
}

// WalkResult contiene resultado de un SNMP WALK
type WalkResult struct {
	OID   string
	Value string
}

// Walk realiza SNMP WALK de un OID base
func (sc *SNMPClient) Walk(baseOID string, ctx *Context) ([]WalkResult, error) {
	client, err := sc.connect()
	if err != nil {
		return nil, err
	}
	defer client.Conn.Close()

	var results []WalkResult

	// gosnmp.WalkFunc es callback para cada OID encontrado
	err = client.Walk(baseOID, func(dataUnit gosnmp.SnmpPDU) error {
		results = append(results, WalkResult{
			OID:   dataUnit.Name,
			Value: ParseValue(dataUnit),
		})
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error en SNMP WALK %s: %w", baseOID, err)
	}

	return results, nil
}

// connect establece conexión SNMP
func (sc *SNMPClient) connect() (*gosnmp.GoSNMP, error) {
	var version gosnmp.SnmpVersion

	switch sc.version {
	case "1":
		version = gosnmp.Version1
	case "2c":
		version = gosnmp.Version2c
	default:
		version = gosnmp.Version2c
	}

	params := &gosnmp.GoSNMP{
		Target:    sc.host,
		Port:      sc.port,
		Community: sc.community,
		Version:   version,
		Timeout:   sc.timeout,
		Retries:   sc.retries,
	}

	err := params.Connect()
	if err != nil {
		return nil, fmt.Errorf("error conectando a %s:%d: %w", sc.host, sc.port, err)
	}

	return params, nil
}

// ParseValue convierte un PDU variable a string
// Maneja diferentes tipos: strings, bytes (con decodificación UTF-8 y MAC), números
func ParseValue(variable gosnmp.SnmpPDU) string {
	if variable.Value == nil {
		return ""
	}

	switch v := variable.Value.(type) {
	case string:
		// Limpiar null terminators
		return strings.TrimRight(v, "\x00")
	case []byte:
		// Primero intentar interpretar como UTF-8 o ASCII válido
		if isValidUTF8(v) && isLikelyText(v) {
			str := string(v)
			// Limpiar null terminators
			return strings.TrimRight(str, "\x00")
		}

		// Si es exactamente 6 bytes y NO es texto válido, asumir que es MAC address binario
		if len(v) == 6 && !isLikelyText(v) {
			hexStr := hex.EncodeToString(v)
			return fmt.Sprintf("%s:%s:%s:%s:%s:%s", hexStr[0:2], hexStr[2:4], hexStr[4:6], hexStr[6:8], hexStr[8:10], hexStr[10:12])
		}

		// Para otros bytes, retornar como string (puede ser ASCII o mostrar hex si es binario)
		// Intentar limpiar si es ASCII imprimible
		if isLikelyASCII(v) {
			return strings.TrimRight(string(v), "\x00")
		}

		// Si no es texto válido, retornar vacío
		return ""
	case int:
		return fmt.Sprintf("%d", v)
	case uint:
		return fmt.Sprintf("%d", v)
	case uint32:
		return fmt.Sprintf("%d", v)
	case uint64:
		return fmt.Sprintf("%d", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// isValidUTF8 valida si un slice de bytes es UTF-8 válido
func isValidUTF8(b []byte) bool {
	for i := 0; i < len(b); {
		if b[i] < 0x80 {
			i++
		} else if b[i] < 0xC0 {
			return false
		} else if b[i] < 0xE0 {
			if i+1 >= len(b) {
				return false
			}
			i += 2
		} else if b[i] < 0xF0 {
			if i+2 >= len(b) {
				return false
			}
			i += 3
		} else if b[i] < 0xF8 {
			if i+3 >= len(b) {
				return false
			}
			i += 4
		} else {
			return false
		}
	}
	return true
}

// isLikelyText verifica si bytes parecen ser texto (no caracteres de control raros)
func isLikelyText(b []byte) bool {
	if len(b) == 0 {
		return false
	}

	// Contar cuántos bytes son caracteres imprimibles o espacios en blanco
	printableCount := 0
	for _, c := range b {
		// ASCII printable: 32-126, más tab/newline/carriage return
		if (c >= 32 && c <= 126) || c == 9 || c == 10 || c == 13 {
			printableCount++
		}
	}

	// Si al menos el 80% de los bytes son imprimibles, parece texto
	return float64(printableCount)/float64(len(b)) >= 0.8
}

// isLikelyASCII verifica si los bytes son ASCII imprimibles o espacios
func isLikelyASCII(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		// ASCII imprimible: 32-126, más whitespace: 9,10,13
		if c < 32 && c != 9 && c != 10 && c != 13 {
			return false
		}
		if c > 126 {
			return false
		}
	}
	return true
}

func (sc *SNMPClient) PingDiscovery() (string, string, error) {
	client, err := sc.connect()
	if err != nil {
		return "", "", err
	}
	defer client.Conn.Close()

	// OIDs: sysDescr (1.0) y sysObjectID (2.0)
	result, err := client.Get([]string{"1.3.6.1.2.1.1.1.0", "1.3.6.1.2.1.1.2.0"})

	if err != nil {
		return "", "", fmt.Errorf("timeout o error SNMP: %w", err)
	}

	if result == nil || len(result.Variables) == 0 {
		return "", "", fmt.Errorf("sin respuesta")
	}

	if result.Error != gosnmp.NoError {
		return "", "", fmt.Errorf("SNMP error %d", result.Error)
	}

	var sysDescr, sysObjectID string

	// Procesar la primera variable (sysDescr)
	if len(result.Variables) > 0 && result.Variables[0].Value != nil {
		sysDescr = ParseValue(result.Variables[0])
	}

	// Procesar la segunda variable (sysObjectID)
	if len(result.Variables) > 1 && result.Variables[1].Value != nil {
		sysObjectID = ParseValue(result.Variables[1])
	}

	if sysDescr == "" {
		return "", "", fmt.Errorf("sysdescr vacío")
	}

	return sysDescr, sysObjectID, nil
}

// Context contiene información de contexto para operaciones SNMP
type Context struct {
	Timeout time.Duration
	Retries int
	// Otros campos según sea necesario
}

// NewContext crea un nuevo contexto
func NewContext() *Context {
	return &Context{
		Timeout: 2 * time.Second,
		Retries: 1,
	}
}
