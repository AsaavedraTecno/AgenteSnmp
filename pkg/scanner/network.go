package scanner

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

type IPRangeConfig struct {
	IPFrom string `json:"ip_from"`
	IPTo   string `json:"ip_to"`
	Active bool   `json:"active"`
}

// GenerateIPsFromRanges procesa múltiples rangos y retorna una lista única de IPs
func GenerateIPsFromRanges(ranges []IPRangeConfig) ([]string, error) {
	var allIPs []string
	uniqueMap := make(map[string]bool)

	for _, r := range ranges {
		// Ignorar rangos inactivos
		if !r.Active {
			continue
		}

		// Construimos el string formato "Inicio-Fin" para reutilizar tu lógica existente
		rangeStr := fmt.Sprintf("%s-%s", r.IPFrom, r.IPTo)

		ips, err := ParseIPRange(rangeStr)
		if err != nil {
			// Opción: Loguear error pero continuar con los siguientes rangos
			fmt.Printf("Error parseando rango %s: %v\n", rangeStr, err)
			continue
		}

		// Agregamos al mapa para evitar duplicados automáticamente
		for _, ip := range ips {
			if !uniqueMap[ip] {
				uniqueMap[ip] = true
				allIPs = append(allIPs, ip)
			}
		}
	}

	if len(allIPs) == 0 {
		return nil, fmt.Errorf("no se generaron IPs válidas de los rangos proporcionados")
	}

	return allIPs, nil
}

// ParseIPRange parsea un rango de IPs en formato "192.168.1.1-254"
// Retorna lista de IPs individuales
func ParseIPRange(ipRange string) ([]string, error) {
	// Limpiar espacios por si acaso " 192... - 192... "
	ipRange = strings.ReplaceAll(ipRange, " ", "")

	parts := strings.Split(ipRange, "-")

	if len(parts) == 1 {
		// Caso IP individual: "192.168.1.50"
		if net.ParseIP(ipRange) != nil {
			return []string{ipRange}, nil
		}
		return nil, fmt.Errorf("formato de IP inválido: %s", ipRange)
	}

	if len(parts) == 2 {
		startStr := parts[0]
		endStr := parts[1]

		// Lógica híbrida: ¿El final es una IP o un número simple?
		finalSuffix, err := resolveEndSuffix(startStr, endStr)
		if err != nil {
			return nil, err
		}

		// Ahora que ya sabemos que 'finalSuffix' es un número (ej: "100"),
		// llamamos a la lógica original.
		return parseRangeFormat(startStr, finalSuffix)
	}

	return nil, fmt.Errorf("formato de rango inválido: %s", ipRange)
}

// resolveEndSuffix determina el octeto final ya sea que venga como "100" o "192.168.1.100"
func resolveEndSuffix(startIPStr, endPartStr string) (string, error) {
	// 1. Intentar parsear el inicio para tener referencia
	startIP := net.ParseIP(startIPStr)
	if startIP == nil {
		return "", fmt.Errorf("IP inicial inválida: %s", startIPStr)
	}
	startIPv4 := startIP.To4()
	if startIPv4 == nil {
		return "", fmt.Errorf("solo IPv4 soportada")
	}

	// 2. ¿La segunda parte es una IP completa? (ej: 192.168.1.100)
	endIP := net.ParseIP(endPartStr)
	if endIP != nil {
		endIPv4 := endIP.To4()
		if endIPv4 == nil {
			return "", fmt.Errorf("IP final inválida")
		}

		// Validación de seguridad: Deben estar en la misma subred /24 por ahora
		// Comparamos los primeros 3 octetos
		if startIPv4[0] != endIPv4[0] || startIPv4[1] != endIPv4[1] || startIPv4[2] != endIPv4[2] {
			return "", fmt.Errorf("el rango cruza subredes diferentes (%s -> %s), no soportado en este modo", startIPStr, endPartStr)
		}

		// Retornamos solo el último octeto como string (ej: "100")
		return fmt.Sprintf("%d", endIPv4[3]), nil
	}

	// 3. Si no es IP, asumimos que es el número directo (ej: "100")
	// Validamos que sea numérico
	if _, err := strconv.Atoi(endPartStr); err != nil {
		return "", fmt.Errorf("el final del rango no es ni una IP ni un número válido: %s", endPartStr)
	}

	return endPartStr, nil
}

// parseRangeFormat genera la lista de IPs iterando el último octeto
func parseRangeFormat(startIP, endOctet string) ([]string, error) {
	// Parsear IP inicial
	ip := net.ParseIP(startIP)
	// (Ya validamos IP antes, pero doble check no daña)
	if ip == nil {
		return nil, fmt.Errorf("IP inicial inválida")
	}

	ipv4 := ip.To4()
	startNum := int(ipv4[3])

	// Parsear octeto final
	endNum, err := strconv.Atoi(endOctet)
	if err != nil {
		return nil, fmt.Errorf("error convirtiendo octeto final: %v", err)
	}

	if endNum < startNum {
		return nil, fmt.Errorf("rango inválido: el final (%d) es menor al inicio (%d)", endNum, startNum)
	}

	if endNum > 255 {
		return nil, fmt.Errorf("octeto fuera de rango (>255): %d", endNum)
	}

	// Generar lista
	var ips []string
	for i := startNum; i <= endNum; i++ {
		// Reconstruimos la IP manteniendo los primeros 3 octetos fijos
		newIP := net.IPv4(ipv4[0], ipv4[1], ipv4[2], byte(i))
		ips = append(ips, newIP.String())
	}

	return ips, nil
}
