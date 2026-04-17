package discovery

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/asaavedra/agent-snmp/pkg/snmp"
)

// DeviceIdentity contiene los marcadores nativos del hardware, tal como los
// reporta el propio dispositivo. Son la fuente de verdad para la detección de marca.
type DeviceIdentity struct {
	// SysObjectID es el OID de identidad del fabricante (ej. "1.3.6.1.4.1.253.8.53").
	// Su prefijo (enterprise OID) identifica unívocamente al fabricante.
	SysObjectID string
	// SysDescr es la descripción libre del sistema (ej. "Xerox WorkCentre 3225 ...").
	// Contiene modelo, versión de firmware y otras cadenas útiles para detección.
	SysDescr string
}

// identifyResult es el carrier interno del resultado asíncrono del goroutine.
type identifyResult struct {
	identity *DeviceIdentity
	err      error
}

// Discoverer es el Motor Fase Cero: su única responsabilidad es determinar
// si una IP es una impresora SNMP administrable y retornar su identidad.
// Está configurado con tolerancias estrictas para no bloquear el pool de recolección.
type Discoverer struct {
	IP          string
	Port        uint16
	Community   string
	SNMPVersion string
	// Timeout por intento SNMP individual. Se recomienda 2-3 segundos máximo.
	Timeout time.Duration
	// Retries: máximo 1 para cumplir la política "Fail Fast".
	// Si la impresora no responde en el primer reintento, no es un target válido.
	Retries int
}

// NewDiscoverer construye el motor con defaults de "Fail Fast":
// - 3 segundos de timeout por intento
// - 1 solo reintento
// Total máximo bloqueante: ~6 segundos antes de liberar la goroutine del pool.
func NewDiscoverer(ip, community, version string, port uint16) *Discoverer {
	return &Discoverer{
		IP:          ip,
		Port:        port,
		Community:   community,
		SNMPVersion: version,
		Timeout:     3 * time.Second,
		Retries:     1,
	}
}

// Identify ejecuta el GET de identificación (sysObjectID + sysDescr) y retorna
// DeviceIdentity si la IP es un dispositivo SNMP válido.
//
// El ctx externo actúa como "kill switch" global: si el agente decide cancelar
// el job completo (ej. shutdown, timeout de ronda), esta función se aborta
// limpiamente sin goroutine leaks.
//
// Internamente también aplica su propio deadline para proteger al pool
// incluso si el ctx externo no tiene timeout propio.
func (d *Discoverer) Identify(ctx context.Context) (*DeviceIdentity, error) {
	// Deadline interno: (timeout * (retries+1)) + 1s de margen para TCP handshake.
	// Garantiza que esta función NUNCA bloquee más que este tiempo,
	// independientemente del ctx que llegue desde fuera.
	internalDeadline := d.Timeout*time.Duration(d.Retries+1) + time.Second
	reqCtx, cancel := context.WithTimeout(ctx, internalDeadline)
	defer cancel()

	// Lanzamos la operación SNMP en un goroutine separado porque gosnmp no
	// soporta context.Context nativamente: su timeout es a nivel de conexión TCP,
	// no de operación Go. Esto nos permite honrar ctx.Done() correctamente.
	resultCh := make(chan identifyResult, 1)

	go func() {
		identity, err := d.doIdentify()
		resultCh <- identifyResult{identity: identity, err: err}
	}()

	select {
	case res := <-resultCh:
		return res.identity, res.err
	case <-reqCtx.Done():
		// El contexto se canceló (timeout interno o shutdown del agente).
		// El goroutine terminará solo cuando gosnmp agote su propio TCP timeout.
		return nil, fmt.Errorf("fase_cero_cancelado [ip=%s]: %w", d.IP, reqCtx.Err())
	}
}

// doIdentify es la operación SNMP síncrona ejecutada dentro del goroutine.
// Hace un GET de los dos OIDs de identidad estándar RFC 1213.
func (d *Discoverer) doIdentify() (*DeviceIdentity, error) {
	client := snmp.NewSNMPClient(
		d.IP,
		d.Port,
		d.Community,
		d.SNMPVersion,
		d.Timeout,
		d.Retries,
	)

	// Los dos OIDs universales de identidad (presentes en cualquier dispositivo SNMP v1+).
	const (
		oidSysObjectID = "1.3.6.1.2.1.1.2.0"
		oidSysDescr    = "1.3.6.1.2.1.1.1.0"
	)

	results, err := client.GetMultiple(
		[]string{oidSysObjectID, oidSysDescr},
		snmp.NewContext(),
	)
	if err != nil {
		return nil, fmt.Errorf("fase_cero_error_snmp [ip=%s]: %w", d.IP, err)
	}

	identity := &DeviceIdentity{}

	if val, ok := results[oidSysObjectID]; ok && val != nil {
		identity.SysObjectID = cleanSNMPString(val)
	}
	if val, ok := results[oidSysDescr]; ok && val != nil {
		identity.SysDescr = cleanSNMPString(val)
	}

	// Un dispositivo sin sysObjectID no es una impresora administrable.
	// Podría ser un switch, cámara, o simplemente no soportar la MIB necesaria.
	if identity.SysObjectID == "" {
		return nil, fmt.Errorf("fase_cero_no_identificable [ip=%s]: sysObjectID vacío", d.IP)
	}

	return identity, nil
}

// cleanSNMPString normaliza cualquier valor SNMP a un string limpio sin whitespace exterior.
func cleanSNMPString(val interface{}) string {
	return strings.TrimSpace(fmt.Sprintf("%v", val))
}
