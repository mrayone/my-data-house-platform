// Command producer é o gerador de carga CDC sintética em Avro: simula os
// eventos que, em produção, viriam do SAP via Datasphere/CDC para os tópicos
// Confluent.
//
// cmd é fino por convenção (ADR-0009): este arquivo só faz parsing de flag e
// wiring. Toda lógica vive em internal/.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/mrayone/my-data-house-platform/internal/platform/logging"
	"github.com/mrayone/my-data-house-platform/internal/platform/mockrun"
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
	case "mock":
		runMock(log, os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "comando desconhecido: %s\n", os.Args[1])
		os.Exit(1)
	}
}

// runMock gera o dataset sintético completo da PoC como NDJSON, um arquivo
// por entidade. É o stand-in explícito para o producer Avro + Kafka real
// (Fase 03 — ver internal/platform/mockrun e docs/plan/PROGRESS.md,
// Bloqueios #3/#4): mesmo layout de colunas final, transporte diferente.
func runMock(log *slog.Logger, args []string) {
	fs := flag.NewFlagSet("producer mock", flag.ExitOnError)
	outDir := fs.String("out", "./out/mock", "diretorio de saida dos arquivos NDJSON")
	baseFlag := fs.String("base", "", "instante base (RFC3339); default: agora")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	base := time.Now().UTC()
	if *baseFlag != "" {
		parsed, err := time.Parse(time.RFC3339, *baseFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "producer mock: --base invalido: %v\n", err)
			os.Exit(1)
		}
		base = parsed.UTC()
	}

	datasets := mockrun.All(base)
	if err := mockrun.WriteNDJSON(*outDir, datasets); err != nil {
		fmt.Fprintf(os.Stderr, "producer mock: %v\n", err)
		os.Exit(1)
	}

	total := 0
	for _, d := range datasets {
		total += len(d.Records)
		log.Info("producer mock", "context", d.Context, "entity", d.Entity,
			"records", len(d.Records), "file", d.FileName(), "landing_table", d.LandingTable())
	}
	fmt.Printf("producer mock: %d datasets, %d registros escritos em %s\n", len(datasets), total, *outDir)
}
