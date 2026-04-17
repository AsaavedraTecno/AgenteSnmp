package profile

import (
	"strings"
)

// FriendlyNameResolver genera nombres legibles para OIDs
type FriendlyNameResolver struct {
	knownOIDs map[string]string // OID -> FriendlyName
}

// NewFriendlyNameResolver crea un nuevo resolver
func NewFriendlyNameResolver() *FriendlyNameResolver {
	return &FriendlyNameResolver{
		knownOIDs: initializeKnownOIDs(),
	}
}

// GetFriendlyName retorna un nombre legible para un OID
func (fnr *FriendlyNameResolver) GetFriendlyName(oid string) string {
	// Buscar en base de datos conocidos
	if name, ok := fnr.knownOIDs[oid]; ok {
		return name
	}

	// Generar automático basado en patrones
	return fnr.generateFriendlyName(oid)
}

// generateFriendlyName genera un nombre basado en patrones del OID
func (fnr *FriendlyNameResolver) generateFriendlyName(oid string) string {
	// Extraer la última parte numérica
	parts := strings.Split(oid, ".")
	if len(parts) < 2 {
		return "Unknown OID"
	}

	// Patrones RFC 3805
	if strings.Contains(oid, ".43.10.2.1.4") {
		// Contadores de páginas
		if strings.HasSuffix(oid, ".1") {
			return "Total Pages"
		} else if strings.HasSuffix(oid, ".2") {
			return "Monochrome Pages"
		} else if strings.HasSuffix(oid, ".3") {
			return "Color Pages"
		} else if strings.HasSuffix(oid, ".4") {
			return "Scan Pages"
		} else if strings.HasSuffix(oid, ".5") {
			return "Copy Pages"
		} else if strings.HasSuffix(oid, ".6") {
			return "Fax Pages"
		}
		return "Page Counter"
	}

	// Consumibles RFC 3805
	if strings.Contains(oid, ".43.11") {
		return "Supply"
	}

	// Estado
	if strings.Contains(oid, ".43.5") || strings.Contains(oid, ".43.13") {
		return "Printer Status"
	}

	// Uptime / Sistema
	if strings.Contains(oid, ".1.3.0") {
		return "System Uptime"
	}

	// MAC Address
	if strings.Contains(oid, ".2.1.6") {
		return "MAC Address"
	}

	// Hostname
	if strings.Contains(oid, ".1.5.0") {
		return "Hostname"
	}

	// Descripción del sistema
	if strings.Contains(oid, ".1.1.0") {
		return "System Description"
	}

	// Modelo
	if strings.Contains(oid, ".25.3.2.1.3") {
		return "Printer Model"
	}

	// Serial Number
	if strings.Contains(oid, ".43.5.1.1.17") {
		return "Serial Number"
	}

	// Fabricante
	if strings.Contains(oid, ".1.2.0") {
		return "Manufacturer"
	}

	// Localización
	if strings.Contains(oid, ".1.6.0") {
		return "Location"
	}

	// Contacto
	if strings.Contains(oid, ".1.4.0") {
		return "Contact"
	}

	// Genérico: usar última parte del OID
	if len(parts) > 0 {
		lastPart := parts[len(parts)-1]
		return "OID " + lastPart
	}

	return "Unknown OID"
}

// initializeKnownOIDs crea la base de datos de OIDs conocidos
func initializeKnownOIDs() map[string]string {
	return map[string]string{
		// RFC 3805 - Printer MIB
		"1.3.6.1.2.1.43.10.2.1.4.1.1": "Total Page Count",
		"1.3.6.1.2.1.43.10.2.1.4.1.2": "Monochrome Page Count",
		"1.3.6.1.2.1.43.10.2.1.4.1.3": "Color Page Count",
		"1.3.6.1.2.1.43.10.2.1.4.1.4": "Scan Page Count",
		"1.3.6.1.2.1.43.10.2.1.4.1.5": "Copy Page Count",
		"1.3.6.1.2.1.43.10.2.1.4.1.6": "Fax Page Count",
		"1.3.6.1.2.1.43.11.1.1.6.1.1": "Supply Toner Level",
		"1.3.6.1.2.1.43.11.1.1.8.1.1": "Supply Max Capacity",
		"1.3.6.1.2.1.43.5.1.1.2.1":    "Printer State",
		"1.3.6.1.2.1.43.13.0":         "Printer Alerts",

		// System MIB
		"1.3.6.1.2.1.1.1.0": "System Description",
		"1.3.6.1.2.1.1.2.0": "System OID",
		"1.3.6.1.2.1.1.3.0": "System Uptime",
		"1.3.6.1.2.1.1.4.0": "System Contact",
		"1.3.6.1.2.1.1.5.0": "System Name (Hostname)",
		"1.3.6.1.2.1.1.6.0": "System Location",

		// Host Resources MIB
		"1.3.6.1.2.1.25.3.2.1.3.1": "Printer Model",
		"1.3.6.1.2.1.25.3.2.1.5.1": "Device Status",

		// Network Interface MIB
		"1.3.6.1.2.1.2.1.2.1":   "Interface 1 Description",
		"1.3.6.1.2.1.2.2.1.2.1": "Interface 1 Name",
		"1.3.6.1.2.1.2.2.1.6.1": "Interface 1 MAC Address",

		// IP MIB
		"1.3.6.1.2.1.4.20.1.1": "IP Address",

		// HP Enterprise OIDs
		".1.3.6.1.4.1.11.2.3.9.4.2.1.4.1.1":     "HP Total Impressions",
		".1.3.6.1.4.1.11.2.3.9.4.2.1.4.1.2.7.0": "HP Color Impressions",
		".1.3.6.1.4.1.11.2.3.9.4.3.1.1.8.1.1":   "HP Toner Cartridge",

		// Xerox Enterprise OIDs
		"1.3.6.1.4.1.253.8.53.3.2.1.5.1.1.1": "Xerox Total Pages",
		"1.3.6.1.4.1.253.8.53.13.0":          "Xerox Supply Status",

		// Samsung Enterprise OIDs
		"1.3.6.1.4.1.236.11.5.1.1.4.1.1":  "Samsung Total Pages",
		"1.3.6.1.4.1.236.11.5.11.1.1.6.1": "Samsung Toner Level",

		// Ricoh Enterprise OIDs
		"1.3.6.1.4.1.367.3.2.1.5.1.1.1": "Ricoh Total Pages",
		"1.3.6.1.4.1.367.3.2.1.5.1.1.2": "Ricoh Color Pages",
	}
}

// AddCustomMapping permite agregar mappings personalizados
func (fnr *FriendlyNameResolver) AddCustomMapping(oid, friendlyName string) {
	fnr.knownOIDs[oid] = friendlyName
}

// DetectOIDType intenta determinar el tipo de OID basado en su nombre
func (fnr *FriendlyNameResolver) DetectOIDType(friendlyName string) string {
	nameUpper := strings.ToUpper(friendlyName)

	// Contadores
	if strings.Contains(nameUpper, "PAGE") || strings.Contains(nameUpper, "COUNT") ||
		strings.Contains(nameUpper, "IMPRESSION") || strings.Contains(nameUpper, "SCAN") ||
		strings.Contains(nameUpper, "COPY") || strings.Contains(nameUpper, "FAX") {
		return "counter"
	}

	// Consumibles
	if strings.Contains(nameUpper, "TONER") || strings.Contains(nameUpper, "SUPPLY") ||
		strings.Contains(nameUpper, "DRUM") || strings.Contains(nameUpper, "FUSER") ||
		strings.Contains(nameUpper, "ROLLER") || strings.Contains(nameUpper, "PAD") ||
		strings.Contains(nameUpper, "INK") {
		return "supplies"
	}

	// Estado
	if strings.Contains(nameUpper, "STATUS") || strings.Contains(nameUpper, "STATE") ||
		strings.Contains(nameUpper, "ALERT") || strings.Contains(nameUpper, "ERROR") ||
		strings.Contains(nameUpper, "WARNING") {
		return "status"
	}

	// Red
	if strings.Contains(nameUpper, "MAC") || strings.Contains(nameUpper, "IP") ||
		strings.Contains(nameUpper, "NETWORK") || strings.Contains(nameUpper, "INTERFACE") ||
		strings.Contains(nameUpper, "ADDRESS") {
		return "network"
	}

	// Sistema
	if strings.Contains(nameUpper, "HOSTNAME") || strings.Contains(nameUpper, "UPTIME") ||
		strings.Contains(nameUpper, "SYSTEM") || strings.Contains(nameUpper, "DESCRIPTION") ||
		strings.Contains(nameUpper, "LOCATION") || strings.Contains(nameUpper, "CONTACT") {
		return "system"
	}

	// Default
	return "vendor"
}

// GetUnit retorna la unidad para un OID basado en su tipo
func (fnr *FriendlyNameResolver) GetUnit(friendlyName string) string {
	nameUpper := strings.ToUpper(friendlyName)

	// Porcentajes
	if strings.Contains(nameUpper, "LEVEL") || strings.Contains(nameUpper, "CAPACITY") {
		return "%"
	}

	// Páginas
	if strings.Contains(nameUpper, "PAGE") || strings.Contains(nameUpper, "IMPRESSION") ||
		strings.Contains(nameUpper, "SCAN") || strings.Contains(nameUpper, "COPY") ||
		strings.Contains(nameUpper, "FAX") || strings.Contains(nameUpper, "COUNT") {
		return "pages"
	}

	// Tiempo
	if strings.Contains(nameUpper, "UPTIME") || strings.Contains(nameUpper, "TIME") {
		return "seconds"
	}

	// Genérico
	return ""
}
