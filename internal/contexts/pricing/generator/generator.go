// Package generator produz carga CDC sintética para pricing/discount_codes e
// pricing/prices. Ver internal/contexts/pricing/CLAUDE.md.
package generator

import (
	"time"

	"github.com/mrayone/my-data-house-platform/internal/platform/mockgen"
)

const (
	TopicDiscountCodes = "sap.pricing.discount_codes.v1"
	TopicPrices        = "sap.pricing.prices.v1"
)

// DiscountCodes gera o cadastro de cupons. Inclui as três variações de
// PROMO10 documentadas em sales/CLAUDE.md para testar normalização de cupom
// (maiúsculas/minúsculas e espaços) — o cadastro em si guarda só a forma
// canônica "PROMO10"; as variações vivem no lado do pedido (order.go).
func DiscountCodes(base time.Time) []mockgen.Record {
	var seq mockgen.SeqCounter
	rows := []struct {
		code, campaign, discType string
		value                    float64
		buScope                  any
		maxUses                  int64
	}{
		{"PROMO10", "Aniversário 10%", "percent", 10, nil, 5000},
		{"FRETEGRATIS", "Frete grátis", "freight", 0, nil, 20000},
		{"BU01-EXCLUSIVE", "Campanha exclusiva BU-01", "fixed", 15, "BU-01", 500},
		// "CUPOM-INEXISTENTE" é usado por sales/order.go como discount_code
		// órfão deliberado (achado do cenário 004) — não é criado aqui.
	}

	validFrom := base.AddDate(0, -6, 0)
	validTo := base.AddDate(1, 0, 0)

	out := make([]mockgen.Record, 0, len(rows))
	for _, r := range rows {
		env := mockgen.Envelope{
			Topic:    TopicDiscountCodes,
			KafkaTS:  base,
			Op:       mockgen.OpCreate,
			CDCSeq:   seq.Next(),
			SchemaID: 1,
		}
		biz := mockgen.Record{
			"discount_code":       r.code,
			"campaign_name":       r.campaign,
			"discount_type":       r.discType,
			"discount_value":      mockgen.Decimal(r.value, 4),
			"valid_from":          mockgen.FormatTimestamp(validFrom),
			"valid_to":            mockgen.FormatTimestamp(validTo),
			"max_uses":            r.maxUses,
			"business_unit_scope": r.buScope,
			"created_at":          mockgen.FormatTimestamp(base),
			"updated_at":          mockgen.FormatTimestamp(base),
		}
		out = append(out, env.Apply(biz))
	}
	return out
}

// Prices gera duas versões de preço por SKU (uma antiga, vencida em
// valid_to, e uma vigente) — o par mínimo para exercitar o ASOF JOIN do
// cenário 005: um order_item com created_at entre as duas versões deve
// casar com a mais antiga; um com created_at após a mudança deve casar com
// a mais nova.
func Prices(base time.Time) []mockgen.Record {
	var seq mockgen.SeqCounter
	skus := []struct {
		itemID           string
		oldList, oldCost float64
		newList, newCost float64
	}{
		{"SKU-000001", 199.90, 120.00, 219.90, 125.00},
		{"SKU-000002", 89.90, 45.00, 99.90, 48.00},
	}

	priceChangeAt := base.Add(-48 * time.Hour)
	oldValidFrom := base.AddDate(0, -3, 0)

	out := make([]mockgen.Record, 0, len(skus)*2)
	for _, s := range skus {
		envOld := mockgen.Envelope{Topic: TopicPrices, KafkaTS: oldValidFrom, Op: mockgen.OpCreate, CDCSeq: seq.Next(), SchemaID: 1}
		out = append(out, envOld.Apply(mockgen.Record{
			"item_id":       s.itemID,
			"price_list_id": "varejo",
			"currency":      "BRL",
			"list_price":    mockgen.Decimal(s.oldList, 4),
			"cost_price":    mockgen.Decimal(s.oldCost, 4),
			"valid_from":    mockgen.FormatTimestamp(oldValidFrom),
			"valid_to":      mockgen.FormatTimestamp(priceChangeAt),
			"created_at":    mockgen.FormatTimestamp(oldValidFrom),
			"updated_at":    mockgen.FormatTimestamp(priceChangeAt),
		}))

		envNew := mockgen.Envelope{Topic: TopicPrices, KafkaTS: priceChangeAt, Op: mockgen.OpCreate, CDCSeq: seq.Next(), SchemaID: 1}
		out = append(out, envNew.Apply(mockgen.Record{
			"item_id":       s.itemID,
			"price_list_id": "varejo",
			"currency":      "BRL",
			"list_price":    mockgen.Decimal(s.newList, 4),
			"cost_price":    mockgen.Decimal(s.newCost, 4),
			"valid_from":    mockgen.FormatTimestamp(priceChangeAt),
			"valid_to":      nil,
			"created_at":    mockgen.FormatTimestamp(priceChangeAt),
			"updated_at":    mockgen.FormatTimestamp(priceChangeAt),
		}))
	}
	return out
}

// PriceChangeAt expõe o instante da mudança de preço para os testes de
// order_item que precisam gerar um pedido antes e outro depois dela.
func PriceChangeAt(base time.Time) time.Time {
	return base.Add(-48 * time.Hour)
}
