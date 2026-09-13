// Package generator produz carga CDC sintética para customer/customer. Ver
// internal/contexts/customer/CLAUDE.md.
package generator

import (
	"time"

	"github.com/mrayone/my-data-house-platform/internal/platform/mockgen"
)

const Topic = "sap.customer.customer.v1"

// Customers gera clientes, incluindo CUST-000001 — o ID de exemplo referido
// pelos critérios de aceite do cenário 001 e 008 (customer/CLAUDE.md).
func Customers(base time.Time) []mockgen.Record {
	var seq mockgen.SeqCounter
	rows := []struct {
		id, first, segment, tier, city, state, zip string
		sinceMonthsAgo                             int
	}{
		{"CUST-000001", "Ana", "b2c", "gold", "São Paulo", "SP", "01310-100", 30},
		{"CUST-000002", "Bruno", "b2c", "silver", "Rio de Janeiro", "RJ", "22041-001", 14},
		{"CUST-000003", "Carla", "b2b", "black", "Curitiba", "PR", "80010-000", 6},
	}

	out := make([]mockgen.Record, 0, len(rows))
	for _, r := range rows {
		since := base.AddDate(0, -r.sinceMonthsAgo, 0)
		env := mockgen.Envelope{
			Topic:    Topic,
			KafkaTS:  since,
			Op:       mockgen.OpCreate,
			CDCSeq:   seq.Next(),
			SchemaID: 1,
		}
		biz := mockgen.Record{
			"customer_id":    r.id,
			"document_hash":  "sha256:" + r.id,
			"first_name":     r.first,
			"email_hash":     "sha256:email:" + r.id,
			"customer_since": mockgen.FormatDate(since),
			"segment":        r.segment,
			"loyalty_tier":   r.tier,
			"city":           r.city,
			"state":          r.state,
			"zipcode":        r.zip,
			"created_at":     mockgen.FormatTimestamp(since),
			"updated_at":     mockgen.FormatTimestamp(since),
		}
		out = append(out, env.Apply(biz))
	}
	return out
}
