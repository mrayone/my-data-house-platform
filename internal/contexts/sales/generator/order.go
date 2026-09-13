// Package generator produz carga CDC sintética para o contexto sales (order,
// order_item, order_payment). Os IDs nomeados abaixo são contrato com os
// critérios de aceite das Fases 05-08 e com internal/contexts/sales/CLAUDE.md
// — mudar um deles exige atualizar os docs de cenário no mesmo commit.
package generator

import (
	"time"

	"github.com/mrayone/my-data-house-platform/internal/platform/mockgen"
)

const TopicOrder = "sap.sales.order.v1"

type orderRow struct {
	id, customerID, buCode, status, channel, currency string
	discountCode                                      any
	gross, discount, freight, total                   float64
	createdAt                                         time.Time
}

func baseOrder(o orderRow) mockgen.Record {
	return mockgen.Record{
		"order_id":           o.id,
		"customer_id":        o.customerID,
		"business_unit_code": o.buCode,
		"order_status":       o.status,
		"channel":            o.channel,
		"currency":           o.currency,
		"discount_code":      o.discountCode,
		"gross_amount":       mockgen.Decimal(o.gross, 4),
		"discount_amount":    mockgen.Decimal(o.discount, 4),
		"freight_amount":     mockgen.Decimal(o.freight, 4),
		"total_amount":       mockgen.Decimal(o.total, 4),
		"created_at":         mockgen.FormatTimestamp(o.createdAt),
		"updated_at":         mockgen.FormatTimestamp(o.createdAt),
	}
}

// Orders gera o conjunto de pedidos da PoC: alguns "normais" (para os marts
// agregados terem volume e diversidade de BU/canal/moeda) e os casos
// adversos documentados em sales/CLAUDE.md. Retorna as linhas já na ORDEM DE
// EMISSÃO pretendida (não na ordem de _cdc_seq) — quem chama decide se insere
// nessa ordem (para provar fora-de-ordem) ou reordenada.
func Orders(base time.Time) []mockgen.Record {
	var seq mockgen.SeqCounter
	var out []mockgen.Record

	emit := func(o orderRow, op mockgen.Op, cdcSeq uint64) {
		env := mockgen.Envelope{Topic: TopicOrder, KafkaTS: o.createdAt, Op: op, CDCSeq: cdcSeq, SchemaID: 1}
		out = append(out, env.Apply(baseOrder(o)))
	}

	// --- pedidos "normais", com diversidade de BU/canal/moeda/status -------
	normal := []orderRow{
		{"ORD-000123", "CUST-000001", "BU-01", "delivered", "web", "BRL", "PROMO10", 500, 50, 20, 470, base.AddDate(0, 0, -10)},
		{"ORD-000124", "CUST-000002", "BU-01", "paid", "app", "BRL", nil, 250, 0, 15, 265, base.AddDate(0, 0, -5)},
		{"ORD-000125", "CUST-000003", "BU-02", "shipped", "web", "BRL", "FRETEGRATIS", 800, 0, 0, 800, base.AddDate(0, 0, -3)},
		{"ORD-000126", "CUST-000001", "BU-02", "canceled", "store", "BRL", nil, 120, 0, 10, 130, base.AddDate(0, 0, -2)},
		{"ORD-000127", "CUST-000002", "BU-TEST", "delivered", "web", "BRL", nil, 300, 0, 12, 312, base.AddDate(0, 0, -1)},
	}
	for _, o := range normal {
		emit(o, mockgen.OpCreate, seq.Next())
	}

	// --- ORD-OOO-1: v2 publicada ANTES da v1 (fora de ordem) ---------------
	// v1: created "paid"; v2 (_cdc_seq maior): "canceled". Emitimos v2 no
	// slice ANTES de v1 — quem inserir na ordem do slice está inserindo fora
	// de ordem de propósito. O core deve manter "canceled" (maior _cdc_seq),
	// não "paid" (o que chegou por último na inserção).
	oooBase := orderRow{"ORD-OOO-1", "CUST-000001", "BU-01", "paid", "web", "BRL", nil, 199.9, 0, 10, 209.9, base.AddDate(0, 0, -1)}
	oooV1Seq := seq.Next()
	oooV2Seq := seq.Next()
	oooV2 := oooBase
	oooV2.status = "canceled"
	emit(oooV2, mockgen.OpUpdate, oooV2Seq) // inserida primeiro no slice (fora de ordem)
	emit(oooBase, mockgen.OpCreate, oooV1Seq)

	// --- ORD-DEL-1: delete de CDC -------------------------------------------
	delRow := orderRow{"ORD-DEL-1", "CUST-000002", "BU-01", "paid", "web", "BRL", nil, 89.9, 0, 10, 99.9, base.AddDate(0, 0, -6)}
	emit(delRow, mockgen.OpCreate, seq.Next())
	emit(delRow, mockgen.OpDelete, seq.Next())

	// --- ORD-RES-1: ressurreição após delete --------------------------------
	// delete seguido de um create com _cdc_seq MAIOR — deve reaparecer.
	resRow := orderRow{"ORD-RES-1", "CUST-000003", "BU-02", "paid", "app", "BRL", nil, 149.9, 0, 10, 159.9, base.AddDate(0, 0, -7)}
	emit(resRow, mockgen.OpCreate, seq.Next())
	emit(resRow, mockgen.OpDelete, seq.Next())
	resurrected := resRow
	resurrected.status = "delivered"
	emit(resurrected, mockgen.OpCreate, seq.Next()) // _op='r' também seria válido; 'c' testa o mesmo caminho

	// --- ORD-LATE-1: late arrival — itens chegam ANTES do pedido -----------
	// O pedido em si é normal; a orfandade transitória é produzida por
	// OrderItems emitindo os itens antes deste registro ser inserido.
	late1 := orderRow{"ORD-LATE-1", "CUST-000001", "BU-01", "paid", "web", "BRL", nil, 60, 0, 10, 70, base.AddDate(0, 0, -1)}
	emit(late1, mockgen.OpCreate, seq.Next())

	// --- ORD-LATE-2: late arrival inverso — pedido chega ANTES dos itens ---
	late2 := orderRow{"ORD-LATE-2", "CUST-000002", "BU-01", "paid", "web", "BRL", nil, 45, 0, 10, 55, base.AddDate(0, 0, -1)}
	emit(late2, mockgen.OpCreate, seq.Next())

	// --- cupom órfão deliberado (achado do cenário 004, não bug) -----------
	orphanCoupon := orderRow{"ORD-000128", "CUST-000003", "BU-01", "paid", "web", "BRL", "CUPOM-INEXISTENTE", 200, 0, 10, 210, base.AddDate(0, 0, -4)}
	emit(orphanCoupon, mockgen.OpCreate, seq.Next())

	// --- normalização de cupom: mesma promoção, três grafias -----------------
	promoVariants := []struct{ id, code string }{
		{"ORD-000129", "PROMO10"},
		{"ORD-000130", "promo10"},
		{"ORD-000131", " PROMO10 "},
	}
	for _, v := range promoVariants {
		o := orderRow{v.id, "CUST-000001", "BU-01", "paid", "web", "BRL", v.code, 100, 10, 10, 100, base.AddDate(0, 0, -1)}
		emit(o, mockgen.OpCreate, seq.Next())
	}

	return out
}
