package generator

import (
	"strconv"
	"time"

	"github.com/mrayone/my-data-house-platform/internal/platform/mockgen"
)

// TopicOrderPayment é o tópico Avro de origem desta entidade (sales/order_payment).
const TopicOrderPayment = "sap.sales.order_payment.v1"

type orderPaymentRow struct {
	paymentID     string
	orderID       string
	paymentMethod string
	installments  int32
	paymentStatus string
	amount        float64
	acquirer      string
	authorizedAt  any // time.Time ou nil
	capturedAt    any // time.Time ou nil
	createdAt     time.Time
	updatedAt     time.Time
}

func baseOrderPayment(p orderPaymentRow) mockgen.Record {
	authorizedAt := p.authorizedAt
	if t, ok := authorizedAt.(time.Time); ok {
		authorizedAt = mockgen.FormatTimestamp(t)
	}
	capturedAt := p.capturedAt
	if t, ok := capturedAt.(time.Time); ok {
		capturedAt = mockgen.FormatTimestamp(t)
	}
	return mockgen.Record{
		"payment_id":     p.paymentID,
		"order_id":       p.orderID,
		"payment_method": p.paymentMethod,
		"installments":   p.installments,
		"payment_status": p.paymentStatus,
		"amount":         mockgen.Decimal(p.amount, 4),
		"acquirer":       p.acquirer,
		"authorized_at":  authorizedAt,
		"captured_at":    capturedAt,
		"created_at":     mockgen.FormatTimestamp(p.createdAt),
		"updated_at":     mockgen.FormatTimestamp(p.updatedAt),
	}
}

// OrderPayments gera as tentativas de pagamento usadas no cenário 003 (funil e
// taxa de aprovação) e nas suas armadilhas documentadas em
// docs/scenarios/003-payment-funnel.md.
func OrderPayments(base time.Time) []mockgen.Record {
	var seq mockgen.SeqCounter
	var out []mockgen.Record

	emit := func(p orderPaymentRow, op mockgen.Op, cdcSeq uint64) {
		env := mockgen.Envelope{Topic: TopicOrderPayment, KafkaTS: p.updatedAt, Op: op, CDCSeq: cdcSeq, SchemaID: 1}
		out = append(out, env.Apply(baseOrderPayment(p)))
	}

	// --- ACQ-TEST: dedup de tentativa de pagamento --------------------------
	// Uma única tentativa (payment_id fixo) passa por 3 estados —
	// pending -> authorized -> captured — cada um com _cdc_seq crescente.
	// Critério de aceite: attempts = 1 (não 3) e attempts_captured = 1 após
	// FINAL/dedup pelo ReplacingMergeTree.
	acqTestCreated := base.AddDate(0, 0, -1)
	acqTestAuthAt := acqTestCreated.Add(30 * time.Second)
	acqTestCapAt := acqTestCreated.Add(90 * time.Second)
	emit(orderPaymentRow{"PAY-ACQTEST-1", "ORD-000123", "credit_card", 3, "pending", 470, "ACQ-TEST", nil, nil, acqTestCreated, acqTestCreated},
		mockgen.OpCreate, seq.Next())
	emit(orderPaymentRow{"PAY-ACQTEST-1", "ORD-000123", "credit_card", 3, "authorized", 470, "ACQ-TEST", acqTestAuthAt, nil, acqTestCreated, acqTestAuthAt},
		mockgen.OpUpdate, seq.Next())
	emit(orderPaymentRow{"PAY-ACQTEST-1", "ORD-000123", "credit_card", 3, "captured", 470, "ACQ-TEST", acqTestAuthAt, acqTestCapAt, acqTestCreated, acqTestCapAt},
		mockgen.OpUpdate, seq.Next())

	// --- ACQ-LAT-TEST: latência de autorização com nulo ---------------------
	// 9 tentativas autorizadas exatamente 10s após created_at + 1 tentativa
	// nunca autorizada (authorized_at nulo). Critério: auth_latency_p50_s ~10,
	// nunca ~0 (que seria o efeito de nulo virando zero em quantile sem If).
	latBase := base.AddDate(0, 0, -1)
	for n := 1; n <= 9; n++ {
		created := latBase.Add(time.Duration(n) * time.Minute)
		authAt := created.Add(10 * time.Second)
		emit(orderPaymentRow{
			paymentID: "PAY-LATTEST-" + strconv.Itoa(n), orderID: "ORD-000124", paymentMethod: "pix",
			installments: 1, paymentStatus: "authorized", amount: 100, acquirer: "ACQ-LAT-TEST",
			authorizedAt: authAt, capturedAt: nil, createdAt: created, updatedAt: authAt,
		}, mockgen.OpCreate, seq.Next())
	}
	pendingCreated := latBase.Add(10 * time.Minute)
	emit(orderPaymentRow{"PAY-LATTEST-10", "ORD-000124", "pix", 1, "pending", 100, "ACQ-LAT-TEST", nil, nil, pendingCreated, pendingCreated},
		mockgen.OpCreate, seq.Next())

	// --- ACQ-RETRY-TEST: fan-out de retentativa -----------------------------
	// 3 tentativas distintas (payment_id diferentes) para o MESMO pedido,
	// mesmo método/adquirente: 2 negadas, a 3ª capturada. Critério: attempts=3,
	// orders_with_attempt=1, max_attempts_per_order=3, orders_with_retry=1.
	retryBase := base.AddDate(0, 0, -3)
	emit(orderPaymentRow{"PAY-RETRY-1", "ORD-000125", "credit_card", 1, "denied", 800, "ACQ-RETRY-TEST", retryBase.Add(5 * time.Second), nil, retryBase, retryBase.Add(5 * time.Second)},
		mockgen.OpCreate, seq.Next())
	retry2 := retryBase.Add(2 * time.Minute)
	emit(orderPaymentRow{"PAY-RETRY-2", "ORD-000125", "credit_card", 1, "denied", 800, "ACQ-RETRY-TEST", retry2.Add(5 * time.Second), nil, retry2, retry2.Add(5 * time.Second)},
		mockgen.OpCreate, seq.Next())
	retry3 := retryBase.Add(4 * time.Minute)
	retry3Auth := retry3.Add(5 * time.Second)
	retry3Cap := retry3.Add(20 * time.Second)
	emit(orderPaymentRow{"PAY-RETRY-3", "ORD-000125", "credit_card", 1, "captured", 800, "ACQ-RETRY-TEST", retry3Auth, retry3Cap, retry3, retry3Cap},
		mockgen.OpCreate, seq.Next())

	// --- Pagamentos "padrão" para os demais pedidos --------------------------
	// Cobrem os pedidos restantes de order.go para o funil não ficar vazio
	// fora dos IDs nomeados; acquirer genérico 'ACQ-STD'.
	std := []orderPaymentRow{
		{"PAY-STD-126", "ORD-000126", "boleto", 1, "denied", 130, "ACQ-STD", nil, nil, base.AddDate(0, 0, -2), base.AddDate(0, 0, -2)},
		{"PAY-STD-127", "ORD-000127", "pix", 1, "captured", 312, "ACQ-STD", base.AddDate(0, 0, -1).Add(20 * time.Second), base.AddDate(0, 0, -1).Add(40 * time.Second), base.AddDate(0, 0, -1), base.AddDate(0, 0, -1).Add(40 * time.Second)},
		{"PAY-STD-128", "ORD-000128", "credit_card", 2, "captured", 210, "ACQ-STD", base.AddDate(0, 0, -4).Add(15 * time.Second), base.AddDate(0, 0, -4).Add(50 * time.Second), base.AddDate(0, 0, -4), base.AddDate(0, 0, -4).Add(50 * time.Second)},
		{"PAY-STD-129", "ORD-000129", "credit_card", 1, "captured", 100, "ACQ-STD", base.AddDate(0, 0, -1).Add(15 * time.Second), base.AddDate(0, 0, -1).Add(50 * time.Second), base.AddDate(0, 0, -1), base.AddDate(0, 0, -1).Add(50 * time.Second)},
		{"PAY-STD-130", "ORD-000130", "credit_card", 1, "captured", 100, "ACQ-STD", base.AddDate(0, 0, -1).Add(15 * time.Second), base.AddDate(0, 0, -1).Add(50 * time.Second), base.AddDate(0, 0, -1), base.AddDate(0, 0, -1).Add(50 * time.Second)},
		{"PAY-STD-131", "ORD-000131", "credit_card", 1, "captured", 100, "ACQ-STD", base.AddDate(0, 0, -1).Add(15 * time.Second), base.AddDate(0, 0, -1).Add(50 * time.Second), base.AddDate(0, 0, -1), base.AddDate(0, 0, -1).Add(50 * time.Second)},
	}
	for _, p := range std {
		emit(p, mockgen.OpCreate, seq.Next())
	}

	return out
}
