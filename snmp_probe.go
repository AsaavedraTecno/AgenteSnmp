package main

import (
	"fmt"
	"os"
	"time"

	"github.com/asaavedra/agent-snmp/pkg/snmp"
)

func main() {
	ip := "192.168.150.20"
	community := "public"
	baseOID := "1.3.6.1.4.1.236.11.5.11"

	if len(os.Args) >= 2 {
		ip = os.Args[1]
	}
	if len(os.Args) >= 3 {
		community = os.Args[2]
	}
	if len(os.Args) >= 4 {
		baseOID = os.Args[3]
	}

	fmt.Printf("🔍 SNMP Probe\n")
	fmt.Printf("   IP        : %s\n", ip)
	fmt.Printf("   Community : %s\n", community)
	fmt.Printf("   Base OID  : %s\n\n", baseOID)

	client := snmp.NewSNMPClient(ip, 161, community, "2c", 3*time.Second, 1)
	ctx := snmp.NewContext()

	results, err := client.Walk(baseOID, ctx)
	if err != nil {
		fmt.Printf("❌ Error: %v\n", err)
		os.Exit(1)
	}

	if len(results) == 0 {
		fmt.Println("⚠️  Sin resultados. El OID no responde en este equipo.")
		fmt.Println("\nProbando OIDs alternativos Samsung...")
		probeAlternatives(client, ctx, ip)
		return
	}

	fmt.Printf("✅ %d OIDs encontrados:\n\n", len(results))
	fmt.Printf("%-55s %s\n", "OID", "VALOR")
	fmt.Printf("%-55s %s\n", "---", "-----")

	for _, r := range results {
		fmt.Printf("%-55s %v\n", r.OID, r.Value)
	}

	fmt.Println("\n💡 Busca los valores que parezcan contadores de páginas (números grandes)")
}

func probeAlternatives(client *snmp.SNMPClient, ctx *snmp.Context, ip string) {
	candidates := []struct {
		desc string
		oid  string
	}{
		{"Total páginas (nuevo)", "1.3.6.1.4.1.236.11.5.11.1.1.0"},
		{"Total páginas (legacy)", "1.3.6.1.4.1.236.11.5.1.1.3.27.0"},
		{"Mono prints", "1.3.6.1.4.1.236.11.5.11.1.5.0"},
		{"Mono legacy", "1.3.6.1.4.1.236.11.5.1.1.3.29.0"},
		{"Mono RFC Samsung", "1.3.6.1.4.1.236.11.5.11.53.11.1.2.0"},
		{"Color RFC Samsung", "1.3.6.1.4.1.236.11.5.11.53.11.1.1.0"},
		{"Total RFC estándar", "1.3.6.1.2.1.43.10.2.1.4.1.1"},
		{"Scan pages", "1.3.6.1.4.1.236.11.5.11.1.19.0"},
		{"Copy pages", "1.3.6.1.4.1.236.11.5.11.1.14.0"},
		{"Fax pages", "1.3.6.1.4.1.236.11.5.11.1.24.0"},
		{"Total árbol 5.1", "1.3.6.1.4.1.236.11.5.1.1.3.1.0"},
		{"Total árbol 5.1 alt", "1.3.6.1.4.1.236.11.5.1.1.3.2.0"},
	}

	oids := make([]string, len(candidates))
	for i, c := range candidates {
		oids[i] = c.oid
	}

	results, err := client.GetMultiple(oids, ctx)
	if err != nil {
		fmt.Printf("❌ Error en probe alternativo: %v\n", err)
		return
	}

	fmt.Printf("\n%-45s %-55s %s\n", "DESCRIPCIÓN", "OID", "VALOR")
	fmt.Printf("%-45s %-55s %s\n", "-----------", "---", "-----")

	for _, c := range candidates {
		val, ok := results[c.oid]
		if ok && val != nil {
			fmt.Printf("%-45s %-55s %v\n", c.desc, c.oid, val)
		} else {
			fmt.Printf("%-45s %-55s (sin respuesta)\n", c.desc, c.oid)
		}
	}
}
