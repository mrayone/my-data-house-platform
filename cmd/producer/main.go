// Command producer é o gerador de carga CDC sintética em Avro: simula os
// eventos que, em produção, viriam do SAP via Datasphere/CDC para os tópicos
// Confluent.
//
// cmd é fino por convenção (ADR-0009): este arquivo só faz parsing de flag e
// wiring. Toda lógica vive em internal/.
package main

import (
	"fmt"
	"os"

	"github.com/mrayone/my-data-house-platform/internal/platform/logging"
	"github.com/mrayone/my-data-house-platform/internal/platform/version"
)

func main() {
	log := logging.New()

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "uso: producer <comando>")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "version":
		log.Info("producer", "version", version.Version)
		fmt.Println("producer", version.Version)
	default:
		fmt.Fprintf(os.Stderr, "comando desconhecido: %s\n", os.Args[1])
		os.Exit(1)
	}
}
