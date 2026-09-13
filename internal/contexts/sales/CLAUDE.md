# CLAUDE.md — contexto `sales`

## O que é

Os fatos transacionais do pedido, replicados do SAP via Datasphere. É o contexto
central da PoC: quase todo cenário passa por aqui.

## Entidades

| Entidade | Tópico | Chave | Natureza | Partições |
|---|---|---|---|---|
| `order` | `sap.sales.order.v1` | `order_id` | **mutável** — o status muda 5× no ciclo de vida | 6 |
| `order_item` | `sap.sales.order_item.v1` | `order_id, item_seq` | **mutável** — quantidade e valor mudam antes do faturamento | 12 |
| `order_payment` | `sap.sales.order_payment.v1` | `payment_id` | **mutável, 1:N** por pedido | 6 |

Contratos em [`../../../contracts/domains/sales/`](../../../contracts/domains/sales/).

## Relações declaradas

| De | Para | `kind` | Liga por | Nota |
|---|---|---|---|---|
| `order` | `customer/customer` | `foreign_key` | `customer_id` | limite de órfão: 0,1% |
| `order` | `organization/business_unit` | `lookup` | `business_unit_code` → `bu_code` | resolvido por `dictGet` |
| `order` | `pricing/discount_codes` | `lookup` | `discount_code` | limite de 5% **de propósito** — cupom órfão é o achado do cenário 004 |
| `order_item` | `sales/order` | `foreign_key` | `order_id` | relação central do cenário 009 |
| `order_item` | `pricing/prices` | `temporal` | `item_id` + tempo | resolvido por `ASOF JOIN` (cenário 005) |
| `order_item` | `inventory/stock_position` | `lookup` | `item_id, dc_id` | chave composta, `dc_id` nulável |
| `order_payment` | `sales/order` | `foreign_key` | `order_id` | fan-out 1:N |

## Cenários que consomem este contexto

001 (360 do pedido) · 002 (receita por BU) · 003 (funil de pagamento) ·
004 (cupom) · 005 (margem) · 006 (demanda) · 007 (ruptura) · 008 (cliente) ·
009 (qualidade) · 010 (catálogo parado) — **todos**.

## Armadilhas específicas deste contexto

1. **`order_item` chega antes de `order`.** É normal — partições e connectors
   independentes ([ADR-0004](../../../docs/adr/0004-semantica-cdc-dedup-ordem-delete.md) §7).
   Nada descarta órfão; a orfandade é medida (cenário 009). Marts usam Refreshable
   MV para se autocorrigirem.
2. **Join com `order_item` sem agregar antes multiplica o cabeçalho.** Um pedido de
   5 itens contribuiria com 5× o `total_amount`. Agregue por `order_id` **antes** do
   join, sempre.
3. **`order_payment` é 1:N.** Contar pedidos com `count()` em vez de
   `countDistinct(order_id)` transforma "pedidos" em "tentativas" (cenário 003).
4. **Cancelamento reescreve o passado.** Pedido cancelado hoje muda a receita de
   meses atrás e o `lifetime_net` do cliente. É o motivo de o cenário 008 não ter
   janela de recálculo.
5. **`total_amount` do cabeçalho vs soma dos itens.** Divergência é achado
   (`dq.marts.header_item_parity`), não algo a normalizar em silêncio.
6. **`order_item.dc_id` é `Nullable`** — nulo enquanto o pedido não é alocado.
   Cenários 006 e 007 declaram como tratar; não invente `coalesce` silencioso.

## IDs nomeados produzidos por este contexto

Contrato com os testes das Fases 05-08. Mudá-los quebra critérios de aceite — se
precisar mudar, atualize os docs de cenário no mesmo commit.

| ID | O que exercita |
|---|---|
| `ORD-OOO-1` | v2 publicada antes da v1 (fora de ordem) |
| `ORD-DEL-1` | delete de CDC |
| `ORD-RES-1` | ressurreição após delete |
| `ORD-ORPHAN` | item sem pedido (órfão) |
| `ORD-LATE-1` | late arrival para o e2e de autocorreção |
| `BU-TEST` | BU isolada para asserções de receita |
| `ACQ-TEST` | dedup de tentativa de pagamento |
| `ACQ-LAT-TEST` | latência de autorização com nulo |
| `ACQ-RETRY-TEST` | fan-out de retentativa |
| `PROMO10` / `promo10` / `" PROMO10 "` | normalização de cupom |

## Regras

- Nada cross-context aqui. SQL deste contexto só referencia `dh_*.sales__*` e
  dictionaries. Cruzou contexto? Vai para `analytics`
  ([ADR-0009](../../../docs/adr/0009-layout-de-pastas-context-first.md)).
- Toda leitura de L1 é por `dh_core.v_sales__<entidade>_current`.
- Dinheiro é `Decimal(18,4)` em L0/L1 e `Decimal(38,4)` em agregado. Nunca `Float`.
