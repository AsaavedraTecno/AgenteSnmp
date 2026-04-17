package detector

import (
	"strings"
)

// DetectBrand detecta la marca de una impresora basándose en sysDescr
func DetectBrand(sysDescr string) string {
	descLower := strings.ToLower(sysDescr)

	// HP
	if matchesPatterns(descLower, []string{"hp", "hewlett packard", "laserjet", "officejet", "color laserjet"}) {
		return "HP"
	}

	// Xerox
	if matchesPatterns(descLower, []string{"xerox", "docucentre", "workcentre", "docucolor", "versalink"}) {
		return "Xerox"
	}
	// Brother
	if matchesPatterns(descLower, []string{"brother", "hl-", "mfc-", "dcpl"}) {
		return "Brother"
	}

	// Ricoh
	if matchesPatterns(descLower, []string{"ricoh", "imagio", "lanier", "gestetner"}) {
		return "Ricoh"
	}
	// Canon
	if matchesPatterns(descLower, []string{"canon", "imagerunner", "ir-"}) {
		return "Canon"
	}

	// Konica Minolta
	if matchesPatterns(descLower, []string{"konica", "minolta", "bizhub", "accurio"}) {
		return "KonicaMinolta"
	}

	// OKI
	if matchesPatterns(descLower, []string{"oki", "okidata", "c931", "c941"}) {
		return "OKI"
	}

	// Kyocera
	if matchesPatterns(descLower, []string{"kyocera", "mita", "taskalfa", "km-"}) {
		return "Kyocera"
	}

	// Sharp
	if matchesPatterns(descLower, []string{"sharp", "mx-", "ar-"}) {
		return "Sharp"
	}

	// Toshiba
	if matchesPatterns(descLower, []string{"toshiba", "e-studio"}) {
		return "Toshiba"
	}

	// Samsung
	if matchesPatterns(descLower, []string{"samsung", "ml-", "sl-", "clp-"}) {
		return "Samsung"
	}

	// Generic / Unknown
	return "Generic"
}

// matchesPatterns verifica si descLower contiene alguno de los patrones
func matchesPatterns(descLower string, patterns []string) bool {
	for _, pattern := range patterns {
		if strings.Contains(descLower, pattern) {
			return true
		}
	}
	return false
}

// GetBrandConfidence retorna un valor de confianza (0-1) basado en qué tan específico fue el match
func GetBrandConfidence(sysDescr string, brand string) float64 {
	descLower := strings.ToLower(sysDescr)

	switch brand {
	case "HP":
		if strings.Contains(descLower, "hewlett packard") {
			return 0.99
		} else if strings.Contains(descLower, "hp") && strings.Contains(descLower, "laserjet") {
			return 0.98
		} else if strings.Contains(descLower, "hp") {
			return 0.85
		}
	case "Xerox":
		if strings.Contains(descLower, "xerox") && strings.Contains(descLower, "workcentre") {
			return 0.99
		} else if strings.Contains(descLower, "xerox") {
			return 0.98
		}
	case "Brother":
		if strings.Contains(descLower, "brother") && (strings.Contains(descLower, "hl-") || strings.Contains(descLower, "mfc-")) {
			return 0.99
		} else if strings.Contains(descLower, "brother") {
			return 0.95
		}
	case "Ricoh":
		if strings.Contains(descLower, "ricoh") {
			return 0.95
		} else if strings.Contains(descLower, "imagio") {
			return 0.90
		}
	case "Canon":
		if strings.Contains(descLower, "canon") && strings.Contains(descLower, "imagerunner") {
			return 0.98
		} else if strings.Contains(descLower, "canon") {
			return 0.95
		}
	case "Samsung":
		if strings.Contains(descLower, "samsung") && (strings.Contains(descLower, "ml-") || strings.Contains(descLower, "clp-")) {
			return 0.99
		} else if strings.Contains(descLower, "samsung") {
			return 0.96
		}
	case "Generic":
		return 0.50 // Baja confianza para Generic
	}

	return 0.75 // Confianza por defecto
}
