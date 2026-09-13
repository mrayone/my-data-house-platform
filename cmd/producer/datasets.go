package main

import (
	"time"

	customergen "github.com/mrayone/my-data-house-platform/internal/contexts/customer/generator"
	inventorygen "github.com/mrayone/my-data-house-platform/internal/contexts/inventory/generator"
	organizationgen "github.com/mrayone/my-data-house-platform/internal/contexts/organization/generator"
	pricinggen "github.com/mrayone/my-data-house-platform/internal/contexts/pricing/generator"
	salesgen "github.com/mrayone/my-data-house-platform/internal/contexts/sales/generator"
	"github.com/mrayone/my-data-house-platform/internal/platform/mockrun"
)

// allDatasets monta o dataset completo da PoC. É wiring (ADR-0009): o cmd é
// o único lugar que pode enxergar todos os contextos ao mesmo tempo —
// internal/platform não conhece contexto e um contexto não importa outro.
//
// A ordem retornada é deliberadamente "adversa" em alguns pontos (ex.:
// sales/order_item antes de sales/order) para exercitar os cenários de
// órfão transitório e late arrival documentados em sales/CLAUDE.md e no
// cenário 009 — quem carrega estes datasets decide a ordem de emissão.
//
// KeyFields espelha a chave de negócio do contrato
// (contracts/domains/<ctx>/<entidade>.yaml); na Fase 02 o dhctl passa a
// derivá-la do próprio contrato.
func allDatasets(base time.Time) []mockrun.Dataset {
	return []mockrun.Dataset{
		{Context: "organization", Entity: "business_unit", KeyFields: []string{"bu_code"}, Records: organizationgen.BusinessUnits(base)},
		{Context: "pricing", Entity: "discount_codes", KeyFields: []string{"discount_code"}, Records: pricinggen.DiscountCodes(base)},
		{Context: "pricing", Entity: "prices", KeyFields: []string{"item_id", "price_list_id", "valid_from"}, Records: pricinggen.Prices(base)},
		{Context: "customer", Entity: "customer", KeyFields: []string{"customer_id"}, Records: customergen.Customers(base)},
		{Context: "inventory", Entity: "stock_position", KeyFields: []string{"item_id", "dc_id"}, Records: inventorygen.StockPositions(base)},
		{Context: "sales", Entity: "order_item", KeyFields: []string{"order_id", "item_seq"}, Records: salesgen.OrderItems(base)},
		{Context: "sales", Entity: "order", KeyFields: []string{"order_id"}, Records: salesgen.Orders(base)},
		{Context: "sales", Entity: "order_payment", KeyFields: []string{"payment_id"}, Records: salesgen.OrderPayments(base)},
	}
}
