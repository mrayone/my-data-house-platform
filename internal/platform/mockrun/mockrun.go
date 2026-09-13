// Package mockrun orquestra a geração e a emissão em NDJSON dos datasets
// sintéticos de todos os contextos da PoC.
//
// Isto é o "spike de teste de fluxo": sem um broker Kafka alcançável neste
// ambiente (ver docs/plan/PROGRESS.md, Bloqueios #3/#4), o NDJSON gerado aqui
// é inserido em dh_landing.<ctx>__<entidade>_raw via
// `clickhouse-client ... FORMAT JSONEachRow`, como stand-in explícito para o
// producer Avro + Kafka Connect Sink reais da Fase 03. O formato do Record
// (mockgen.Record) já é o layout final de coluna — landing, core e MVs — e
// não muda quando o transporte real (Avro/Kafka) for implementado.
package mockrun

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mrayone/my-data-house-platform/internal/platform/mockgen"
)

// Dataset agrupa os registros gerados de uma entidade com o contexto/entidade
// a que pertencem, para nomear o arquivo de saída e a tabela de destino.
//
// KeyFields são as colunas de chave de negócio do contrato
// (contracts/domains/<ctx>/<entidade>.yaml) — viram a key da mensagem Kafka,
// garantindo ordem por chave dentro da partição. No spike a lista vive aqui;
// na Fase 02 ela passa a ser lida do contrato pelo dhctl.
type Dataset struct {
	Context   string
	Entity    string
	KeyFields []string
	Records   []mockgen.Record
}

// LandingTable retorna o nome totalmente qualificado da tabela de landing
// (L0) que recebe este dataset — dh_landing.<contexto>__<entidade>_raw.
func (d Dataset) LandingTable() string {
	return fmt.Sprintf("dh_landing.%s__%s_raw", d.Context, d.Entity)
}

// FileName retorna o nome do arquivo NDJSON de saída — <contexto>__<entidade>.jsonl.
func (d Dataset) FileName() string {
	return fmt.Sprintf("%s__%s.jsonl", d.Context, d.Entity)
}

// WriteNDJSON grava cada dataset em <dir>/<Dataset.FileName()>, um registro
// JSON por linha, pronto para `clickhouse-client --query "INSERT INTO
// <LandingTable()> FORMAT JSONEachRow" < arquivo`.
func WriteNDJSON(dir string, datasets []Dataset) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mockrun: criando diretorio de saida %q: %w", dir, err)
	}
	for _, d := range datasets {
		buf, err := mockgen.WriteNDJSON(d.Records)
		if err != nil {
			return fmt.Errorf("mockrun: %s/%s: %w", d.Context, d.Entity, err)
		}
		path := filepath.Join(dir, d.FileName())
		if err := os.WriteFile(path, buf, 0o644); err != nil {
			return fmt.Errorf("mockrun: escrevendo %q: %w", path, err)
		}
	}
	return nil
}
