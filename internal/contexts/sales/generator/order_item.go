package generator

import (
	"time"

	"github.com/mrayone/my-data-house-platform/internal/platform/mockgen"
)

// TopicOrderItem é o tópico Avro de origem desta entidade (sales/order_item).
const TopicOrderItem = "sap.sales.order_item.v1"

type orderItemRow struct {
	orderID   string
	itemSeq   int32
	itemID    string
	dcID      any // string ou nil (Nullable — pedido ainda não alocado)
	quantity  int32
	unitPrice float64
	gross     float64
	discount  float64
	total     float64
	createdAt time.Time
}

func baseOrderItem(i orderItemRow) mockgen.Record {
	return mockgen.Record{
		"order_id":        i.orderID,
		"item_seq":        i.itemSeq,
		"item_id":         i.itemID,
		"dc_id":           i.dcID,
		"quantity":        i.quantity,
		"unit_price":      mockgen.Decimal(i.unitPrice, 4),
		"gross_amount":    mockgen.Decimal(i.gross, 4),
		"discount_amount": mockgen.Decimal(i.discount, 4),
		"total_amount":    mockgen.Decimal(i.total, 4),
		"created_at":      mockgen.FormatTimestamp(i.createdAt),
		"updated_at":      mockgen.FormatTimestamp(i.createdAt),
	}
}

// OrderItems gera os itens de pedido usados nos cenários da PoC.
//
// Convenção deste gerador: total_amount do item é gross_amount - discount_amount
// (sem frete — frete é só de cabeçalho), para que a soma dos itens feche com o
// cabeçalho de order.go em operação normal (regra 5 de sales/CLAUDE.md). O
// gerador não introduz divergência de propósito; se aparecer uma no teste real,
// é achado (dq.marts.header_item_parity), não bug deste código.
func OrderItems(base time.Time) []mockgen.Record {
	var seq mockgen.SeqCounter
	var out []mockgen.Record

	emit := func(i orderItemRow, op mockgen.Op, cdcSeq uint64) {
		env := mockgen.Envelope{Topic: TopicOrderItem, KafkaTS: i.createdAt, Op: op, CDCSeq: cdcSeq, SchemaID: 1}
		out = append(out, env.Apply(baseOrderItem(i)))
	}

	// Itens dos pedidos "normais" (order.go). ORD-000123 e ORD-000125 são
	// criados antes de pricing.PriceChangeAt(base) (base - 48h) e exercitam o
	// lado "preço antigo" do ASOF JOIN do cenário 005; ORD-000127 é criado
	// depois e exercita o lado "preço novo". As combinações item_id/dc_id
	// batem com inventory/generator.go (SKU-000001/DC-01, SKU-000001/DC-02,
	// SKU-000002/DC-01) para exercitar o lookup composto de stock_position.
	normal := []orderItemRow{
		{"ORD-000123", 1, "SKU-000001", "DC-01", 2, 250, 500, 50, 450, base.AddDate(0, 0, -10)},
		{"ORD-000124", 1, "SKU-000002", "DC-01", 1, 250, 250, 0, 250, base.AddDate(0, 0, -5)},
		{"ORD-000125", 1, "SKU-000001", "DC-02", 4, 200, 800, 0, 800, base.AddDate(0, 0, -3)},
		// dc_id nulo: pedido ainda não alocado a um centro de distribuição
		// (armadilha 6 de sales/CLAUDE.md — nada de coalesce silencioso).
		{"ORD-000126", 1, "SKU-000002", nil, 1, 120, 120, 0, 120, base.AddDate(0, 0, -2)},
		{"ORD-000127", 1, "SKU-000001", "DC-01", 1, 300, 300, 0, 300, base.AddDate(0, 0, -1)},
	}
	for _, i := range normal {
		emit(i, mockgen.OpCreate, seq.Next())
	}

	// ORD-000128: item do pedido com cupom órfão (o cabeçalho já carrega o
	// achado; o item só existe para fechar a paridade cabeçalho/item).
	emit(orderItemRow{"ORD-000128", 1, "SKU-000001", "DC-01", 2, 100, 200, 0, 200, base.AddDate(0, 0, -4)},
		mockgen.OpCreate, seq.Next())

	// ORD-000129/130/131: mesma variação de cupom (PROMO10 / promo10 / " PROMO10 ").
	promoVariants := []string{"ORD-000129", "ORD-000130", "ORD-000131"}
	for _, id := range promoVariants {
		emit(orderItemRow{id, 1, "SKU-000002", "DC-01", 1, 100, 100, 10, 90, base.AddDate(0, 0, -1)},
			mockgen.OpCreate, seq.Next())
	}

	// ORD-LATE-1 / ORD-LATE-2: itens correspondentes aos pedidos de late
	// arrival de order.go. A ORDEM DE CARGA (item antes ou depois do pedido)
	// é decidida por quem consome esta lista (harness de teste / loader), não
	// aqui — o gerador só produz o dado, coerente com o cabeçalho.
	emit(orderItemRow{"ORD-LATE-1", 1, "SKU-000001", "DC-01", 1, 60, 60, 0, 60, base.AddDate(0, 0, -1)},
		mockgen.OpCreate, seq.Next())
	emit(orderItemRow{"ORD-LATE-2", 1, "SKU-000002", "DC-01", 1, 45, 45, 0, 45, base.AddDate(0, 0, -1)},
		mockgen.OpCreate, seq.Next())

	// ORD-ORPHAN: item cujo order_id NUNCA aparece em order.go — órfão
	// transitório/permanente proposital para o cenário 009 (anti-join
	// sales.order_item -> sales.order).
	emit(orderItemRow{"ORD-ORPHAN", 1, "SKU-000001", "DC-01", 1, 199.9, 199.9, 0, 199.9, base},
		mockgen.OpCreate, seq.Next())

	return out
}
