# Cenário 005 — Margem real vs preço de tabela

- **Perfil:** analítico
- **Pergunta de negócio:** "Para cada item vendido, qual era o preço de tabela **vigente na data do pedido**, quanto de desconto foi dado de fato, e qual a margem sobre o custo?"
- **Mecanismo:** **`ASOF LEFT JOIN`** dentro de **Refreshable MV** (ADR-0005, árvore de decisão: correlação **temporal/por intervalo** → nenhum agregador incremental expressa isso)
- **Frescor esperado:** ≤ 16 min (`REFRESH EVERY 15 MINUTE`)
- **Janela de recálculo:** 7 dias (maior que o padrão: preço retroativo é comum)
- **Entidades envolvidas:** `sales.order_item`, `sales.order`, `pricing.prices`
- **Tipo de correlação:** **temporal / por intervalo de vigência** — `item_id` + `created_at >= valid_from`
- **ADRs relevantes:** ADR-0001, ADR-0004 (§4, §6, §7), ADR-0005 (§4 `ASOF JOIN`), ADR-0010, ADR-0011

---

## Por que este cenário existe na PoC

Este é o cenário que **nenhum mecanismo incremental resolve**, e é por isso que
ele existe.

A pergunta "qual era o preço de tabela quando o pedido foi feito?" não é um
lookup por chave. É: *entre todas as versões de preço do item `X`, qual tem o
maior `valid_from` que ainda é anterior a `created_at` do item do pedido?*

- **Dictionary não serve:** `dictGet` devolve o preço **vigente agora**, não o
  vigente na data do pedido. Usá-lo aqui reescreveria a margem histórica a cada
  mudança de tabela de preço.
- **MV incremental com `JOIN` não serve:** além de não disparar pelo lado de
  `prices`, ela gravaria o preço vigente no instante do insert — e ficaria errada
  para sempre se o preço chegasse depois (o que acontece: tabela de preço é
  frequentemente publicada com vigência retroativa).
- **`ASOF JOIN` serve, e é nativo.** Resolve "a última versão anterior a" em uma
  operação, sem subquery correlacionada e sem janela manual.

**Risco endereçado:** margem calculada contra o preço errado é o tipo de erro
que passa por revisão e aparece no fechamento contábil. E é também a capacidade
do ClickHouse que mais pesa na comparação com alternativas — vale medir.

---

## Fontes

| Tabela | Objeto lido | Camada | Por que este e não outro |
|---|---|---|---|
| Item do pedido | `dh_core.v_sales__order_item_current` | L1 | Grão do mart. Estado atual: quantidade e preço praticado podem mudar antes do faturamento, e só a última versão conta. |
| Pedido | `dh_core.v_sales__order_current` | L1 | Fornece `business_unit_code`, `currency` e `order_status` (para excluir cancelado do valor). `INNER JOIN` — item sem pedido vai para o cenário 009. |
| Preço de tabela | `dh_core.v_pricing__prices_current` | L1 | Lado direito do `ASOF JOIN`. Precisa ser a view: as N versões da mesma `(item_id, price_list_id, valid_from)` colapsam, senão o `ASOF` escolhe entre duplicatas. |

**Por que `prices` não é dictionary:** um dictionary devolveria uma linha por
chave, e a chave aqui inclui `valid_from` — ou seja, o dictionary teria de conter
todas as vigências e a lógica de escolha continuaria do lado de quem consulta.
`ASOF JOIN` faz isso melhor e sem duplicar o cadastro em memória.

---

## Modelo de saída

```sql
-- internal/contexts/analytics/sql/30-marts/0050__item_margin_daily.sql

CREATE TABLE IF NOT EXISTS dh_marts.item_margin_daily {ON_CLUSTER}
(
    order_date              Date,
    item_id                 String,
    business_unit_code      LowCardinality(String),
    price_list_id           LowCardinality(String),
    currency                LowCardinality(String),

    -- volume
    orders                  UInt64,
    units_sold              Int64,

    -- valores praticados
    revenue_practiced       Decimal(38,4),   -- sum(total_amount) do item
    revenue_at_list         Decimal(38,4),   -- sum(quantity * list_price vigente)
    cost_total              Decimal(38,4),   -- sum(quantity * cost_price vigente)

    -- resultado
    discount_vs_list        Decimal(38,4),   -- revenue_at_list - revenue_practiced
    discount_vs_list_pct    Float64,
    margin_amount           Decimal(38,4),   -- revenue_practiced - cost_total
    margin_pct              Float64,

    -- achados
    units_without_price     Int64,           -- sem preço vigente na data
    units_below_cost        Int64,           -- vendido abaixo do custo
    units_above_list        Int64,           -- vendido acima da tabela

    -- rastreabilidade do ASOF
    price_valid_from_min    Nullable(DateTime64(3)),
    price_valid_from_max    Nullable(DateTime64(3)),
    price_staleness_max_d   Nullable(Int32),  -- idade máx. da vigência usada, em dias

    _refreshed_at           DateTime64(3)
)
ENGINE = ReplacingMergeTree(_refreshed_at)
PARTITION BY toYYYYMM(order_date)
ORDER BY (item_id, order_date, business_unit_code, price_list_id);
```

Decisões do DDL:

- **`ORDER BY (item_id, order_date, ...)`**: o acesso dominante é "como está a
  margem do SKU X". Quem quer "margem do dia, todos os SKUs" ainda se beneficia do
  *partition pruning*.
- **`price_list_id` no grão**: o mesmo item pode ser vendido por listas
  diferentes (varejo, atacado). Agregar entre listas misturaria margens
  incomparáveis.
- **`price_valid_from_*` e `price_staleness_max_d` materializados**: sem eles é
  impossível auditar *qual* preço o `ASOF` escolheu. Numa conta de margem, a
  auditabilidade é parte do entregável.
- **`units_without_price` como contador de unidades**, não de linhas: é a medida
  do impacto real da lacuna de cadastro.

---

## Transformação

```sql
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_marts.mv__item_margin_daily {ON_CLUSTER}
REFRESH EVERY 15 MINUTE APPEND
TO dh_marts.item_margin_daily AS
WITH
    -- janela de 7 dias: preço publicado com vigência retroativa é comum,
    -- e o recálculo precisa alcançar o passado recente (ADR-0005)
    (today() - 7) AS window_from,

    sold AS (
        SELECT
            i.order_id                    AS order_id,
            i.item_seq                    AS item_seq,
            i.item_id                     AS item_id,
            i.quantity                    AS quantity,
            i.unit_price                  AS unit_price,
            i.total_amount                AS total_amount,
            i.created_at                  AS created_at,
            o.business_unit_code          AS business_unit_code,
            o.currency                    AS currency,
            o.order_status                AS order_status
        FROM dh_core.v_sales__order_item_current AS i
        INNER JOIN dh_core.v_sales__order_current AS o USING (order_id)
        WHERE toDate(i.created_at) >= window_from
    ),

    -- lado direito do ASOF: uma linha por (item_id, price_list_id, valid_from).
    -- A view v_*_current já garante isso; explicitamos as colunas usadas.
    price_versions AS (
        SELECT item_id, price_list_id, currency, list_price, cost_price,
               valid_from, valid_to
        FROM dh_core.v_pricing__prices_current
    ),

    priced AS (
        SELECT
            s.*,
            p.price_list_id  AS price_list_id,
            p.list_price     AS list_price,
            p.cost_price     AS cost_price,
            p.valid_from     AS price_valid_from,
            p.valid_to       AS price_valid_to
        FROM sold AS s
        -- ASOF LEFT JOIN: para cada item vendido, a versão de preço com o MAIOR
        -- valid_from que ainda seja <= created_at. A desigualdade é a última
        -- condição do ON, e é obrigatoriamente >= ou > (regra do ASOF).
        ASOF LEFT JOIN price_versions AS p
             ON  s.item_id  = p.item_id
             AND s.currency = p.currency
             AND s.created_at >= p.valid_from
    )
SELECT
    toDate(created_at)                                     AS order_date,
    item_id                                                AS item_id,
    business_unit_code                                     AS business_unit_code,
    -- sem preço vigente, price_list_id vem vazio pelo LEFT: rotulamos
    if(list_price = 0 AND price_valid_from IS NULL, '(sem preco)',
       coalesce(price_list_id, '(sem preco)'))              AS price_list_id,
    currency                                               AS currency,

    countDistinct(order_id)                                AS orders,
    sumIf(quantity, order_status != 'canceled')            AS units_sold,

    sumIf(total_amount, order_status != 'canceled')        AS revenue_practiced,
    -- só soma no "at list" o que TEM preço vigente; senão inflaria/deflacionaria
    sumIf(toDecimal128(quantity, 4) * list_price,
          order_status != 'canceled' AND price_valid_from IS NOT NULL) AS revenue_at_list,
    sumIf(toDecimal128(quantity, 4) * cost_price,
          order_status != 'canceled' AND price_valid_from IS NOT NULL) AS cost_total,

    sumIf(toDecimal128(quantity, 4) * list_price, order_status != 'canceled'
              AND price_valid_from IS NOT NULL)
      - sumIf(total_amount, order_status != 'canceled'
              AND price_valid_from IS NOT NULL)             AS discount_vs_list,

    if(sumIf(toDecimal128(quantity,4) * list_price, order_status != 'canceled'
             AND price_valid_from IS NOT NULL) = 0, 0,
       1 - toFloat64(sumIf(total_amount, order_status != 'canceled'
                           AND price_valid_from IS NOT NULL))
         / toFloat64(sumIf(toDecimal128(quantity,4) * list_price,
                           order_status != 'canceled'
                           AND price_valid_from IS NOT NULL))) AS discount_vs_list_pct,

    sumIf(total_amount, order_status != 'canceled' AND price_valid_from IS NOT NULL)
      - sumIf(toDecimal128(quantity,4) * cost_price, order_status != 'canceled'
              AND price_valid_from IS NOT NULL)              AS margin_amount,

    if(sumIf(total_amount, order_status != 'canceled'
             AND price_valid_from IS NOT NULL) = 0, 0,
       1 - toFloat64(sumIf(toDecimal128(quantity,4) * cost_price,
                           order_status != 'canceled' AND price_valid_from IS NOT NULL))
         / toFloat64(sumIf(total_amount, order_status != 'canceled'
                           AND price_valid_from IS NOT NULL)))  AS margin_pct,

    sumIf(quantity, order_status != 'canceled' AND price_valid_from IS NULL) AS units_without_price,
    sumIf(quantity, order_status != 'canceled' AND price_valid_from IS NOT NULL
                    AND unit_price < cost_price)                AS units_below_cost,
    sumIf(quantity, order_status != 'canceled' AND price_valid_from IS NOT NULL
                    AND unit_price > list_price)                AS units_above_list,

    min(price_valid_from)                                   AS price_valid_from_min,
    max(price_valid_from)                                   AS price_valid_from_max,
    max(dateDiff('day', price_valid_from, created_at))      AS price_staleness_max_d,

    now64(3)                                                AS _refreshed_at

FROM priced
GROUP BY order_date, item_id, business_unit_code, price_list_id, currency;
```

```sql
CREATE VIEW IF NOT EXISTS dh_marts.v_item_margin_daily {ON_CLUSTER} AS
SELECT * EXCEPT (_refreshed_at) FROM dh_marts.item_margin_daily FINAL;

-- internal/contexts/analytics/sql/40-reports/0050__margin.sql
CREATE VIEW IF NOT EXISTS dh_reports.v_items_without_price {ON_CLUSTER} AS
SELECT item_id, min(order_date) AS first_seen, max(order_date) AS last_seen,
       sum(units_without_price) AS units, sum(revenue_practiced) AS revenue_unaudited
FROM dh_marts.v_item_margin_daily
WHERE units_without_price > 0
GROUP BY item_id
ORDER BY units DESC;

CREATE VIEW IF NOT EXISTS dh_reports.v_items_sold_below_cost {ON_CLUSTER} AS
SELECT order_date, item_id, business_unit_code, units_below_cost,
       margin_amount, margin_pct
FROM dh_marts.v_item_margin_daily
WHERE units_below_cost > 0 AND order_date >= today() - 30
ORDER BY margin_amount ASC;
```

---

## Regras de negócio

1. **Grão:** dia × `item_id` × BU × `price_list_id` × moeda.
2. **O preço vigente é o da versão com maior `valid_from <= order_item.created_at`.**
   `valid_to` **não** entra no `ASOF` (o `ASOF` só aceita uma desigualdade), e é
   verificado depois — ver regra 3.
3. **Vigência expirada é achado, não exclusão.** Se o `ASOF` escolheu uma versão
   cujo `valid_to` já passou na data do pedido, a linha permanece e
   `price_staleness_max_d` denuncia. Excluir esconderia a lacuna de cadastro.
4. **Moeda faz parte da condição do `ASOF`.** Preço em BRL nunca é usado para
   item vendido em USD, mesmo que o `item_id` coincida.
5. **Item sem nenhum preço anterior à data** (`ASOF` sem par) não é descartado:
   entra com `price_list_id = '(sem preco)'`, contribui para `units_without_price`
   e para `revenue_practiced`, mas **não** para `revenue_at_list`, `cost_total`,
   `margin_*` nem `discount_vs_list`. Somar zero nesses campos daria margem de
   100%.
6. **Pedido cancelado não entra em nenhum valor**, mas conta em `orders`.
7. **`margin_pct` é sobre a receita praticada** (`1 - custo/receita`), não sobre o
   custo. É a convenção do negócio e está explícita porque as duas definições
   circulam.
8. **`revenue_practiced` usa `total_amount` do item** (já líquido do desconto de
   item), não `quantity * unit_price`. Divergência entre os dois é achado de
   qualidade, não algo a normalizar aqui.
9. **`units_above_list`** (vendido acima da tabela) é tão relevante quanto o
   inverso: indica preço não atualizado ou erro de precificação.
10. **Frete não entra na margem do item.** Frete é do cabeçalho e não é rateado —
    ratear exigiria uma regra de negócio que a origem não fornece.

---

## Armadilhas

### 1. Usar `dictGet` para pegar o preço
Devolve o preço de **agora**. A margem do trimestre passado mudaria a cada nova
tabela de preço publicada. É o erro que este cenário existe para evitar.

### 2. `ASOF JOIN` com a desigualdade fora da última posição
O ClickHouse exige que a condição de desigualdade seja a **última** do `ON`, e
que as anteriores sejam de igualdade. Fora dessa forma, o `ASOF` não é aplicado
(ou o parser recusa) — e um `JOIN` comum silenciosamente multiplicaria a linha por
todas as versões de preço.

### 3. `ASOF` contra a tabela core em vez da view
`prices` é `ReplacingMergeTree`: sem `FINAL`, a mesma
`(item_id, price_list_id, valid_from)` aparece N vezes, e o `ASOF` escolhe uma
qualquer entre as duplicatas — inclusive uma versão que já foi corrigida.

### 4. `LEFT` vs `INNER` no `ASOF`
`ASOF INNER JOIN` descartaria todo item sem preço vigente. O relatório ficaria com
margem ótima e volume menor — plausível e errado. `ASOF LEFT JOIN` mantém a linha e
`units_without_price` conta o buraco.

### 5. Somar `revenue_at_list` para linhas sem preço
`list_price` vem `0` do `LEFT`. Sem o `sumIf(... price_valid_from IS NOT NULL)`,
`discount_vs_list` viraria negativo e `margin_pct` iria a 100%.

### 6. `Decimal` × `Int` sem conversão
`quantity * list_price` mistura `Int32` com `Decimal(18,4)`. A conversão explícita
(`toDecimal128(quantity, 4)`) evita perda de escala e overflow no agregado.

### 7. Preço retroativo fora da janela de refresh
Tabela de preço publicada hoje com `valid_from` de 30 dias atrás **não** corrige a
margem daqueles dias — a janela é de 7. Isso é uma limitação assumida, não um bug:
o alvo `make mart-refresh MART=item_margin_daily WINDOW=30` existe para o
recálculo dirigido, e o procedimento está no runbook de reprocesso.

### 8. Agregador incremental
`order_item` é mutável e `prices` chega depois. Armadilhas 1 e 4 do
`CLAUDE.md` §8, simultaneamente.

---

## Critérios de aceite

- [ ] **O `ASOF` escolhe a vigência correta.** Cadastrar três versões do preço de
  `SKU-ASOF` (`valid_from` = D-10 → 100,00; D-5 → 90,00; D-1 → 80,00) e produzir
  um item vendido em D-3.
  ```sql
  SELECT price_valid_from_max, revenue_at_list / units_sold AS unit_list
  FROM dh_marts.v_item_margin_daily
  WHERE item_id = 'SKU-ASOF' AND order_date = today() - 3;
  -- esperado: unit_list = 90.0000  (a versão de D-5, não a de D-1 nem a de D-10)
  ```
- [ ] **Preço futuro não é usado**: a versão de `valid_from = D-1` não pode afetar
  a venda de D-3 (coberto pela query acima).
- [ ] **Item sem preço não distorce a margem**:
  ```sql
  SELECT units_without_price, revenue_practiced, revenue_at_list, margin_pct
  FROM dh_marts.v_item_margin_daily WHERE item_id = 'SKU-SEM-PRECO';
  -- esperado: units_without_price > 0, revenue_practiced > 0,
  --           revenue_at_list = 0, margin_pct = 0  (nunca 1.0)
  SELECT count() FROM dh_reports.v_items_without_price WHERE item_id = 'SKU-SEM-PRECO';
  -- esperado: 1
  ```
- [ ] **Moeda respeitada**: cadastrar `SKU-FX` com preço só em USD e vender em BRL.
  ```sql
  -- esperado: units_without_price > 0 (o preço USD NÃO é usado)
  ```
- [ ] **Duplicata de preço não afeta o resultado**: produzir 3 versões CDC da mesma
  `(item_id, price_list_id, valid_from)` com `list_price` final 95,00.
  ```sql
  SELECT revenue_at_list / units_sold FROM dh_marts.v_item_margin_daily
  WHERE item_id = 'SKU-DUP' AND order_date = today();
  -- esperado: 95.0000, e units_sold NÃO triplicado
  ```
- [ ] **Venda abaixo do custo detectada**:
  ```sql
  SELECT units_below_cost FROM dh_marts.v_item_margin_daily WHERE item_id = 'SKU-PREJUIZO';
  -- esperado: > 0
  ```
- [ ] **Margem fecha com o cálculo independente**:
  ```sql
  SELECT abs(m.margin - d.margin) AS diff FROM
   (SELECT sum(margin_amount) margin FROM dh_marts.v_item_margin_daily
    WHERE order_date = today()) m,
   (SELECT sum(i.total_amount - toDecimal128(i.quantity,4) * p.cost_price) margin
    FROM dh_core.v_sales__order_item_current i
    INNER JOIN dh_core.v_sales__order_current o USING (order_id)
    ASOF LEFT JOIN (SELECT item_id, currency, cost_price, valid_from
                    FROM dh_core.v_pricing__prices_current) p
      ON i.item_id = p.item_id AND o.currency = p.currency AND i.created_at >= p.valid_from
    WHERE toDate(i.created_at) = today() AND o.order_status != 'canceled'
      AND p.valid_from IS NOT NULL) d;
  -- esperado: 0
  ```
- [ ] **Performance do `ASOF`**: refresh completo da janela de 7 dias em < 30 s no
  dataset de benchmark. Medido em `make bench` e registrado em
  `docs/evaluation/results.md` — é um dos números que pesa na comparação com
  alternativas.
- [ ] **Refresh saudável**.

---

## Checks de qualidade associados

| check_id | Regra | Severidade |
|---|---|---|
| `dq.marts.sum_parity` | margem do mart == cálculo independente com `ASOF` | error |
| `dq.price.coverage` | **novo** — `units_without_price` < 1% das unidades vendidas | error |
| `dq.price.staleness` | **novo** — `price_staleness_max_d` < 365 dias | warn |
| `dq.price.below_cost` | **novo** — unidades vendidas abaixo do custo < 0,5% | warn |
| `dq.marts.refresh_health` | refresh sem exceção | error |
| `dq.money.no_float` | colunas monetárias em `Decimal` | error |

---

## Consumo

```sql
-- margem e desconto efetivo dos 50 SKUs de maior receita no mês
SELECT item_id,
       sum(units_sold)                                          AS units,
       sum(revenue_practiced)                                   AS revenue,
       sum(revenue_at_list)                                     AS revenue_at_list,
       1 - sum(revenue_practiced) / sum(revenue_at_list)         AS discount_pct,
       1 - sum(cost_total)       / sum(revenue_practiced)        AS margin_pct,
       sum(units_without_price)                                  AS units_unaudited
FROM dh_marts.v_item_margin_daily
WHERE order_date >= today() - 30 AND price_list_id != '(sem preco)'
GROUP BY item_id
ORDER BY revenue DESC
LIMIT 50;
```

Percentuais são **recalculados** da razão das somas, nunca pela média das colunas
de percentual do mart.

Relatórios operacionais: `dh_reports.v_items_without_price` (para o time de
cadastro) e `dh_reports.v_items_sold_below_cost` (para pricing).

Sem endpoint aplicacional.

---

## Arquivos no repositório

| Caminho | Conteúdo |
|---|---|
| `internal/contexts/analytics/sql/30-marts/0050__item_margin_daily.sql` | tabela, Refreshable MV com `ASOF`, view |
| `internal/contexts/analytics/sql/40-reports/0050__margin.sql` | `v_items_without_price`, `v_items_sold_below_cost` |
| `internal/contexts/analytics/sql/40-reports/0051__dq_price.sql` | `dq.price.coverage`, `dq.price.staleness`, `dq.price.below_cost` |
| `internal/contexts/analytics/reports/item_margin.go` | query nomeada |
| `internal/contexts/pricing/generator/prices.go` | gerador de múltiplas vigências, preço retroativo, SKU sem preço, preço só em outra moeda |
| `test/e2e/item_margin_test.go` | escolha de vigência, item sem preço, duplicata CDC, moeda |
