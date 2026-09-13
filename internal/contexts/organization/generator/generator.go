// Package generator produz carga CDC sintética para organization/business_unit.
// Ver internal/contexts/organization/CLAUDE.md para as entidades e IDs
// nomeados que este pacote é responsável por produzir.
package generator

import (
	"time"

	"github.com/mrayone/my-data-house-platform/internal/platform/mockgen"
)

const Topic = "sap.organization.business_unit.v1"

// BusinessUnits gera o cadastro de unidades de negócio: BU-01 e BU-02 (uso
// geral nos cenários) e BU-TEST (isolada, usada em asserções de receita que
// não podem ser contaminadas por outras BUs — ver sales/CLAUDE.md).
func BusinessUnits(base time.Time) []mockgen.Record {
	var seq mockgen.SeqCounter
	rows := []struct {
		code, name, channel, region, country, costCenter string
		active                                           bool
	}{
		{"BU-01", "Centauro Digital", "ecommerce", "sudeste", "BR", "CC-1001", true},
		{"BU-02", "Nike.com.br", "ecommerce", "sudeste", "BR", "CC-1002", true},
		{"BU-TEST", "BU de Teste (isolada)", "ecommerce", "sul", "BR", "CC-9999", true},
	}

	out := make([]mockgen.Record, 0, len(rows))
	for _, r := range rows {
		env := mockgen.Envelope{
			Topic:    Topic,
			KafkaTS:  base,
			Op:       mockgen.OpCreate,
			CDCSeq:   seq.Next(),
			SchemaID: 1,
		}
		biz := mockgen.Record{
			"bu_code":     r.code,
			"bu_name":     r.name,
			"channel":     r.channel,
			"region":      r.region,
			"country":     r.country,
			"cost_center": r.costCenter,
			"active":      boolToUint8(r.active),
			"created_at":  mockgen.FormatTimestamp(base),
			"updated_at":  mockgen.FormatTimestamp(base),
		}
		out = append(out, env.Apply(biz))
	}
	return out
}

func boolToUint8(b bool) int {
	if b {
		return 1
	}
	return 0
}
