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
	"time"

	customergen "github.com/mrayone/my-data-house-platform/internal/contexts/customer/generator"
	inventorygen "github.com/mrayone/my-data-house-platform/internal/contexts/inventory/generator"
	organizationgen "github.com/mrayone/my-data-house-platform/internal/contexts/organization/generator"
	pricinggen "github.com/mrayone/my-data-house-platform/internal/contexts/pricing/generator"
	salesgen "github.com/mrayone/my-data-house-platform/internal/contexts/sales/generator"
	"github.com/mrayone/my-data-house-platform/internal/platform/mockgen"
)

// Dataset agrupa os registros gerados de uma entidade com o contexto/entidade
// a que pertencem, para nomear o arquivo de saída e a tabela de destino.
type Dataset struct {
	Context string
	Entity  string
	Records []mockgen.Record
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

// All gera o dataset completo da PoC para o instante base informado.
//
// A ordem retornada é deliberadamente "adversa" em alguns pontos (ex.:
// sales/order_item antes de sales/order) para exercitar os cenários de
// órfão transitório e late arrival documentados em sales/CLAUDE.md e no
// cenário 009 — quem carrega estes datasets no ClickHouse decide a ordem de
// INSERT, esta função só agrupa os dados.
func All(base time.Time) []Dataset {
	return []Dataset{
		{Context: "organization", Entity: "business_unit", Records: organizationgen.BusinessUnits(base)},
		{Context: "pricing", Entity: "discount_codes", Records: pricinggen.DiscountCodes(base)},
		{Context: "pricing", Entity: "prices", Records: pricinggen.Prices(base)},
		{Context: "customer", Entity: "customer", Records: customergen.Customers(base)},
		{Context: "inventory", Entity: "stock_position", Records: inventorygen.StockPositions(base)},
		{Context: "sales", Entity: "order_item", Records: salesgen.OrderItems(base)},
		{Context: "sales", Entity: "order", Records: salesgen.Orders(base)},
		{Context: "sales", Entity: "order_payment", Records: salesgen.OrderPayments(base)},
	}
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
