# CLAUDE.md — contexto `pricing`

## O que é

Preço de tabela e promoções. Contém as **duas formas mais difíceis de correlação**
da PoC: temporal (preço por vigência) e por código string sem integridade
referencial (cupom).

## Entidades

| Entidade | Tópico | Chave | Natureza | Partições |
|---|---|---|---|---|
| `prices` | `sap.pricing.prices.v1` | `item_id, price_list_id, valid_from` | versionado por **vigência** | 6 |
| `discount_codes` | `sap.pricing.discount_codes.v1` | `discount_code` | cadastro pequeno → **Dictionary** | 1 |

Contratos em [`../../../contracts/domains/pricing/`](../../../contracts/domains/pricing/).

## Relações declaradas

| De | Para | `kind` | Liga por | Nota |
|---|---|---|---|---|
| `prices` | `inventory/stock_position` | **`loose`** | `item_id` | conectam-se **só** por `item_id`, sem entidade transacional — base do cenário 010 |
| `discount_codes` | `organization/business_unit` | `lookup` | `business_unit_scope` → `bu_code` | escopo do cupom |

E é alvo de: `sales/order_item` → `prices` (`temporal`) e
`sales/order` → `discount_codes` (`lookup`, limite de 5%).

## Cenários que consomem

004 (efetividade de cupom) · 005 (margem vs tabela) · 010 (catálogo parado).

## `prices`: a correlação temporal

**Este é o caso que nenhum mecanismo incremental resolve.** A pergunta "qual era o
preço de tabela quando o pedido foi feito?" não é lookup por chave — é *entre todas
as versões do item, qual tem o maior `valid_from` ainda anterior ao pedido*.

| Mecanismo | Por que não / por que sim |
|---|---|
| `dictGet` | devolve o preço de **agora**; reescreveria a margem histórica a cada mudança de tabela |
| MV incremental com `JOIN` | não dispara pelo lado de `prices`, e grava errado para sempre se o preço chegar depois (o que acontece: vigência retroativa é comum) |
| **`ASOF JOIN`** | resolve "a última versão anterior a" nativamente, em uma operação |

Duas referências temporais diferentes, e é fácil confundi-las:

| Cenário | Referência | Mecanismo |
|---|---|---|
| 005 — margem | `order_item.created_at` (a **data do pedido**) | `ASOF LEFT JOIN` |
| 010 — catálogo | `now()` (o **preço vigente agora**) | `argMax(..., valid_from)` |

### Armadilhas de `prices`

1. **A desigualdade do `ASOF` tem de ser a última condição do `ON`**; as anteriores
   são igualdades. Fora dessa forma, o ClickHouse não aplica `ASOF` — e um `JOIN`
   comum multiplica a linha por todas as vigências, em silêncio.
2. **`ASOF LEFT`, não `INNER`.** `INNER` descartaria todo item sem preço vigente, e
   o relatório ficaria com margem ótima e volume menor: plausível e errado.
3. **`sumIf(..., price_valid_from IS NOT NULL)` em todo agregado de "at list" e de
   custo.** Sem isso, `list_price` vem `0` do `LEFT` e `margin_pct` vai a 100%.
4. **`ASOF` contra a view, nunca a tabela.** Sem `FINAL`, a mesma vigência aparece
   N vezes e o `ASOF` escolhe entre duplicatas — inclusive uma já corrigida.
5. **Moeda faz parte da condição.** Preço em BRL nunca serve para item vendido em
   USD, mesmo com o `item_id` igual.
6. **`valid_to` não entra no `ASOF`** (só uma desigualdade é permitida). Vigência
   expirada é verificada depois e é **achado**, não exclusão — `price_staleness_max_d`
   denuncia.
7. **Janela de refresh de 7 dias**, não 3: vigência retroativa é comum. Correção
   mais antiga exige `make mart-refresh MART=item_margin_daily WINDOW=30`.

## `discount_codes`: a correlação por código

`order.discount_code` é uma **string digitada pelo cliente**. Não há FK, não há
integridade referencial na origem, e a normalização é de quem consulta.

### Armadilhas de `discount_codes`

1. **Normalize uma vez, antes de qualquer agrupamento:** `upperUTF8(trimBoth(code))`.
   Agrupar pelo código cru transforma `PROMO10`, `promo10` e `" PROMO10"` em três
   campanhas, cada uma com um terço do uso real.
2. **Normalização mínima, e só ela.** Não remova acento, hífen ou espaço interno —
   isso mudaria o código e mascararia um problema de checkout.
3. **Cupom sem cadastro é o achado**, não erro. `dictHas` + `dictGetOrDefault`
   preserva a linha; `INNER JOIN` apagaria exatamente o resultado mais valioso.
   O limite de órfão é 5% **de propósito**.
4. **Vigência e escopo só são avaliáveis com `code_found = 1`.** Contadores em zero
   não significam "válido".
5. **`business_unit_scope` vazio significa "todas as BUs"**, não "sem escopo
   definido". É a semântica da origem, e é o tipo de convenção que se perde.
6. **Cupom usado em pedido cancelado consumiu uso** — por isso `orders` inclui
   cancelados na comparação com `max_uses`.

## IDs nomeados produzidos

| ID | O que exercita |
|---|---|
| `SKU-ASOF` | 3 vigências; a venda de D-3 usa a de D-5 |
| `SKU-SEM-PRECO` | item sem preço vigente (`margin_pct = 0`, nunca 1.0) |
| `SKU-PRECO-EXPIRADO` | vigência com `valid_to` no passado |
| `SKU-VIGENCIA` | duas vigências passadas e uma futura |
| `SKU-DUP` | 3 versões CDC da mesma vigência |
| `SKU-PREJUIZO` | vendido abaixo do custo |
| `SKU-FX` | preço só em outra moeda |
| `SKU-PRECO-SEM-POSICAO` | preço sem posição de estoque (cenário 010) |
| `CUPOM-FANTASMA` | cupom sem cadastro |
| `CUPOM-EXPIRADO` | uso fora da vigência |
| `CUPOM-LIMITE` | uso acima de `max_uses` |

## Regras

- Nada cross-context aqui. Marts que cruzam com `sales` ou `inventory` vivem em
  `analytics`.
- Toda leitura de L1 por `dh_core.v_pricing__<entidade>_current`.
- `list_price` e `cost_price` são `Decimal(18,4)`. Nunca `Float` para dinheiro.
