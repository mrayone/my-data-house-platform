// Package generator produz carga CDC sintética para inventory/stock_position.
// Ver internal/contexts/inventory/CLAUDE.md.
package generator

import (
	"time"

	"github.com/mrayone/my-data-house-platform/internal/platform/mockgen"
)

const Topic = "sap.inventory.stock_position.v1"

// StockPositions gera posições de estoque para os SKUs usados por
// pricing.Prices, em dois centros de distribuição — DC-01 tem estoque
// saudável, DC-02 tem estoque abaixo do safety_stock (base para o alerta do
// cenário 006/007).
func StockPositions(base time.Time) []mockgen.Record {
	var seq mockgen.SeqCounter
	rows := []struct {
		itemID, dcID, dcName     string
		onHand, reserved, safety int64
	}{
		{"SKU-000001", "DC-01", "CD São Paulo", 500, 50, 100},
		{"SKU-000001", "DC-02", "CD Extrema", 20, 15, 100}, // abaixo do safety_stock
		{"SKU-000002", "DC-01", "CD São Paulo", 300, 20, 50},
		// SKU-000003 não tem order_item nenhum — base do cenário 010
		// (capital imobilizado sem venda).
		{"SKU-000003", "DC-01", "CD São Paulo", 1000, 0, 100},
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
			"item_id":      r.itemID,
			"dc_id":        r.dcID,
			"dc_name":      r.dcName,
			"on_hand":      r.onHand,
			"reserved":     r.reserved,
			"available":    r.onHand - r.reserved,
			"safety_stock": r.safety,
			"position_at":  mockgen.FormatTimestamp(base),
			"created_at":   mockgen.FormatTimestamp(base),
			"updated_at":   mockgen.FormatTimestamp(base),
		}
		out = append(out, env.Apply(biz))
	}
	return out
}
