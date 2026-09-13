# Cenário 008 — Métricas de cliente: recência, frequência, ticket e coorte

- **Perfil:** analítico, com leitura pontual aplicacional por `customer_id`
- **Pergunta de negócio:** "Quantos pedidos, em quanto tempo e com que ticket cada cliente compra? E como se comportam as coortes por mês de cadastro?"
- **Mecanismo:** **Refreshable MV** com **substituição total** (ADR-0005; agregação sobre entidade mutável, e o cancelamento reescreve o passado)
- **Frescor esperado:** ≤ 35 min (`REFRESH EVERY 30 MINUTE`)
- **Janela de recálculo:** **nenhuma** — recálculo integral. Ver [§ Por que não há janela](#por-que-não-há-janela)
- **Entidades envolvidas:** `customer.customer`, `sales.order`, `sales.order_item`
- **Tipo de correlação:** por ID (`order.customer_id` → `customer.customer_id`), com agregação na direção do cliente
- **ADRs relevantes:** ADR-0001, ADR-0004 (§4, §6), ADR-0005 (§2), ADR-0010, ADR-0011 (§1, §2)

---

## Por que este cenário existe na PoC

1. **Prova que agregação de histórico completo sobre dado mutável é viável.**
   Todos os outros marts recalculam uma janela de dias. Este não pode: um pedido
   cancelado hoje muda o `total_orders` e o `avg_ticket` **lifetime** do cliente.
   É o caso que expõe o limite da estratégia de janela e força a pergunta de custo.
2. **Prova o padrão de acesso duplo do ADR-0011.** A mesma tabela serve varredura
   analítica (segmentação, coorte) e leitura pontual por `customer_id` (tela de
   atendimento). É onde `PROJECTION` justifica sua existência.
3. **Prova coorte a partir de cadastro, não de primeira compra.** `customer_since`
   vem do SAP; a primeira compra vem das vendas. As duas datas divergem, e a
   divergência é um achado.

---

## Por que não há janela

Uma janela de N dias funciona quando a linha do mart só depende de fatos daquela
janela. Aqui não é o caso:

```
customer C, pedidos:  D-200 (100,00)   D-100 (200,00)   D-2 (300,00)
total_orders = 3   ·   lifetime_net = 600,00   ·   avg_ticket = 200,00

Hoje o pedido de D-200 é cancelado (CDC _op='u', order_status='canceled').
Com janela de 3 dias, o recálculo NÃO vê D-200 → a linha de C não muda.
total_orders continua 3 e lifetime_net continua 600,00.       ✗ ERRADO, para sempre.
```

Portanto: **substituição total**. O custo disso é real e é justamente um dos
números que a PoC precisa medir — `docs/evaluation/results.md` registra a duração
do refresh e o consumo de memória em função do número de clientes. Se o custo for
proibitivo no volume real, a saída documentada é particionar o recálculo por faixa
de `customer_id` (`cityHash64(customer_id) % N`), e isso exigiria um ADR.

---

## Fontes

| Tabela | Objeto lido | Camada | Por que este e não outro |
|---|---|---|---|
| Cliente | `dh_core.v_customer__customer_current` | L1 | Base do `LEFT JOIN`: cliente sem pedido deve aparecer com zeros (é a base da coorte). Estado atual — `segment` e `loyalty_tier` mudam. |
| Pedidos | `dh_core.v_sales__order_current` | L1 | Deduplicado. Ler a tabela core direto contaria cada transição de status como um pedido. |
| Itens | `dh_core.v_sales__order_item_current` | L1 | Para `total_units` e `distinct_items`. Agregado por `customer_id` via `order_id`. |

**Por que `customer` não é dictionary:** é cadastro de porte médio (milhões de
linhas no cenário real) e mutável. A regra do ADR-0005 §3 é cadastro **pequeno e
estável**; um dictionary de milhões de clientes com todos os atributos pressiona a
memória do nó e não traz ganho, já que aqui o cliente é o **grão** e não uma
dimensão de lookup.

---

## Modelo de saída

```sql
-- internal/contexts/analytics/sql/30-marts/0060__customer_metrics.sql

CREATE TABLE IF NOT EXISTS dh_marts.customer_metrics {ON_CLUSTER}
(
    customer_id             String,
    -- atributos do cadastro (snapshot do refresh)
    segment                 LowCardinality(String),
    loyalty_tier            LowCardinality(String),
    state                   LowCardinality(String),
    city                    String,
    customer_since          Date,
    cohort_month            Date,          -- toStartOfMonth(customer_since)

    -- frequência
    total_orders            UInt64,
    orders_canceled         UInt64,
    total_units             Int64,
    distinct_items          UInt64,

    -- valor (lifetime, excluindo cancelados)
    lifetime_gross          Decimal(38,4),
    lifetime_discount       Decimal(38,4),
    lifetime_net            Decimal(38,4),
    avg_ticket              Decimal(38,4),
    max_ticket              Decimal(38,4),

    -- recência
    first_order_at          Nullable(DateTime64(3)),
    last_order_at           Nullable(DateTime64(3)),
    recency_days            Nullable(Int32),
    days_to_first_order     Nullable(Int32),   -- first_order_at - customer_since
    avg_days_between_orders Nullable(Float64),

    -- comportamento
    distinct_business_units UInt64,
    distinct_channels       UInt64,
    orders_with_coupon      UInt64,
    preferred_channel       LowCardinality(String),

    -- RFM simplificado (1-5 por dimensão, calculado no consumo — ver regra 9)
    is_active_90d           UInt8,
    is_one_time_buyer       UInt8,
    never_purchased         UInt8,

    _refreshed_at           DateTime64(3)
)
ENGINE = ReplacingMergeTree(_refreshed_at)
PARTITION BY toYYYYMM(cohort_month)
ORDER BY (customer_id);

-- projection para o acesso analítico por segmento e coorte (ADR-0011 §2).
-- Justificativa: a tabela é ORDER BY customer_id (acesso pontual), mas a
-- segmentação varre por cohort_month/segment e faria full scan sem isto.
ALTER TABLE dh_marts.customer_metrics {ON_CLUSTER}
ADD PROJECTION IF NOT EXISTS p_by_cohort_segment
( SELECT * ORDER BY (cohort_month, segment, loyalty_tier) );

-- (2) coorte agregada: grão mês de cadastro × mês de atividade
CREATE TABLE IF NOT EXISTS dh_marts.customer_cohort_month {ON_CLUSTER}
(
    cohort_month        Date,
    activity_month      Date,
    months_since_signup UInt16,
    segment             LowCardinality(String),
    cohort_size         UInt64,       -- clientes cadastrados na coorte
    active_customers    UInt64,       -- compraram no activity_month
    retention_rate      Float64,
    orders              UInt64,
    net_revenue         Decimal(38,4),
    revenue_per_customer Decimal(38,4),
    _refreshed_at       DateTime64(3)
)
ENGINE = ReplacingMergeTree(_refreshed_at)
PARTITION BY toYYYY(cohort_month)
ORDER BY (cohort_month, activity_month, segment);
```

Decisões do DDL:

- **`ORDER BY (customer_id)`** — perfil primário é leitura pontual (ADR-0011 §1).
  A varredura analítica é atendida pela projection, que é exatamente o caso que o
  ADR-0011 §2 prevê. Uma projection, dentro do limite de duas.
- **`PARTITION BY toYYYYMM(cohort_month)`** e não por `_refreshed_at`: a coorte é
  imutável por cliente, então a partição é estável e o recálculo total reescreve
  todas as partições — o que é aceitável porque o mart é pequeno em relação aos
  fatos.
- **`recency_days` e `avg_days_between_orders` `Nullable`**: cliente sem pedido
  não tem recência. `0` seria "comprou hoje" — o oposto do fato.
- **Flags (`is_active_90d`, `never_purchased`) materializadas**: são os filtros
  mais usados, e materializá-las evita que cada consumidor reimplemente o critério
  de forma ligeiramente diferente.

---

## Transformação

```sql
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_marts.mv__customer_metrics {ON_CLUSTER}
REFRESH EVERY 30 MINUTE APPEND
TO dh_marts.customer_metrics AS
WITH
    -- itens agregados por pedido, para não multiplicar o cabeçalho
    items_per_order AS (
        SELECT order_id, sum(quantity) AS units, countDistinct(item_id) AS items
        FROM dh_core.v_sales__order_item_current
        GROUP BY order_id
    ),

    -- pedidos do cliente, com os itens já colapsados. Histórico COMPLETO.
    cust_orders AS (
        SELECT
            o.customer_id                                   AS customer_id,
            countDistinct(o.order_id)                       AS total_orders,
            countDistinctIf(o.order_id, o.order_status = 'canceled') AS orders_canceled,
            sum(coalesce(ipo.units, 0))                     AS total_units,
            sum(coalesce(ipo.items, 0))                     AS distinct_items,

            sumIf(o.gross_amount,    o.order_status != 'canceled') AS lifetime_gross,
            sumIf(o.discount_amount, o.order_status != 'canceled') AS lifetime_discount,
            sumIf(o.total_amount,    o.order_status != 'canceled') AS lifetime_net,
            maxIf(o.total_amount,    o.order_status != 'canceled') AS max_ticket,
            countDistinctIf(o.order_id, o.order_status != 'canceled') AS orders_valid,

            minIf(o.created_at, o.order_status != 'canceled') AS first_order_at,
            maxIf(o.created_at, o.order_status != 'canceled') AS last_order_at,

            countDistinct(o.business_unit_code)             AS distinct_business_units,
            countDistinct(o.channel)                        AS distinct_channels,
            countDistinctIf(o.order_id, o.discount_code IS NOT NULL
                            AND trimBoth(o.discount_code) != '') AS orders_with_coupon,
            -- canal mais frequente; empate resolvido arbitrariamente por argMax
            argMax(o.channel, 1)                            AS any_channel,
            topKWeighted(1)(o.channel, 1)                   AS top_channel_arr
        FROM dh_core.v_sales__order_current AS o
        LEFT JOIN items_per_order AS ipo USING (order_id)
        GROUP BY o.customer_id
    )
SELECT
    c.customer_id                                           AS customer_id,
    c.segment                                               AS segment,
    c.loyalty_tier                                          AS loyalty_tier,
    c.state                                                 AS state,
    c.city                                                  AS city,
    c.customer_since                                        AS customer_since,
    toStartOfMonth(c.customer_since)                        AS cohort_month,

    coalesce(co.total_orders, 0)                            AS total_orders,
    coalesce(co.orders_canceled, 0)                         AS orders_canceled,
    coalesce(co.total_units, 0)                             AS total_units,
    coalesce(co.distinct_items, 0)                          AS distinct_items,

    coalesce(co.lifetime_gross,    toDecimal128(0,4))       AS lifetime_gross,
    coalesce(co.lifetime_discount, toDecimal128(0,4))       AS lifetime_discount,
    coalesce(co.lifetime_net,      toDecimal128(0,4))       AS lifetime_net,
    if(coalesce(co.orders_valid, 0) = 0, toDecimal128(0,4),
       co.lifetime_net / co.orders_valid)                   AS avg_ticket,
    coalesce(co.max_ticket, toDecimal128(0,4))              AS max_ticket,

    co.first_order_at                                       AS first_order_at,
    co.last_order_at                                        AS last_order_at,
    -- NULL quando nunca comprou: 0 significaria "comprou hoje"
    if(co.last_order_at IS NULL, NULL,
       dateDiff('day', co.last_order_at, now()))            AS recency_days,
    if(co.first_order_at IS NULL, NULL,
       dateDiff('day', toDateTime(c.customer_since), co.first_order_at)) AS days_to_first_order,
    -- média de intervalo só faz sentido com 2+ pedidos válidos
    if(coalesce(co.orders_valid, 0) < 2, NULL,
       dateDiff('day', co.first_order_at, co.last_order_at)
         / (co.orders_valid - 1))                           AS avg_days_between_orders,

    coalesce(co.distinct_business_units, 0)                  AS distinct_business_units,
    coalesce(co.distinct_channels, 0)                        AS distinct_channels,
    coalesce(co.orders_with_coupon, 0)                       AS orders_with_coupon,
    coalesce(arrayElement(co.top_channel_arr, 1), '')        AS preferred_channel,

    if(co.last_order_at IS NOT NULL
       AND co.last_order_at >= now() - INTERVAL 90 DAY, 1, 0) AS is_active_90d,
    if(coalesce(co.orders_valid, 0) = 1, 1, 0)               AS is_one_time_buyer,
    if(coalesce(co.orders_valid, 0) = 0, 1, 0)               AS never_purchased,

    now64(3)                                                 AS _refreshed_at

-- LEFT do lado do CLIENTE: cliente sem pedido precisa existir na tabela
FROM dh_core.v_customer__customer_current AS c
LEFT JOIN cust_orders AS co USING (customer_id);
```

```sql
CREATE VIEW IF NOT EXISTS dh_marts.v_customer_metrics {ON_CLUSTER} AS
SELECT * EXCEPT (_refreshed_at) FROM dh_marts.customer_metrics FINAL;
```

```sql
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_marts.mv__customer_cohort_month {ON_CLUSTER}
REFRESH EVERY 30 MINUTE APPEND
TO dh_marts.customer_cohort_month AS
WITH
    cohort_sizes AS (
        SELECT toStartOfMonth(customer_since) AS cohort_month, segment,
               countDistinct(customer_id) AS cohort_size
        FROM dh_core.v_customer__customer_current
        GROUP BY cohort_month, segment
    ),
    activity AS (
        SELECT
            toStartOfMonth(c.customer_since)     AS cohort_month,
            c.segment                            AS segment,
            toStartOfMonth(o.created_at)         AS activity_month,
            countDistinct(o.customer_id)         AS active_customers,
            countDistinct(o.order_id)            AS orders,
            sum(o.total_amount)                  AS net_revenue
        FROM dh_core.v_sales__order_current AS o
        INNER JOIN dh_core.v_customer__customer_current AS c USING (customer_id)
        WHERE o.order_status != 'canceled'
        GROUP BY cohort_month, segment, activity_month
    )
SELECT
    a.cohort_month                                              AS cohort_month,
    a.activity_month                                            AS activity_month,
    dateDiff('month', a.cohort_month, a.activity_month)         AS months_since_signup,
    a.segment                                                   AS segment,
    s.cohort_size                                               AS cohort_size,
    a.active_customers                                          AS active_customers,
    if(s.cohort_size = 0, 0, a.active_customers / s.cohort_size) AS retention_rate,
    a.orders                                                    AS orders,
    a.net_revenue                                               AS net_revenue,
    if(a.active_customers = 0, toDecimal128(0,4),
       a.net_revenue / a.active_customers)                      AS revenue_per_customer,
    now64(3)                                                    AS _refreshed_at
FROM activity AS a
INNER JOIN cohort_sizes AS s USING (cohort_month, segment);
```

---

## Regras de negócio

1. **Grão da tabela 1:** um cliente. **Todo** cliente do cadastro aparece,
   inclusive quem nunca comprou (`never_purchased = 1`) — sem isso a coorte fica
   errada, porque o denominador da retenção é o tamanho da coorte, não o número de
   compradores.
2. **Valores lifetime excluem pedidos cancelados**; `total_orders` os inclui, e
   `orders_canceled` os isola. `avg_ticket` usa apenas pedidos válidos no
   numerador e no denominador.
3. **`recency_days`, `days_to_first_order` e `avg_days_between_orders` são nulos
   quando indefinidos**, nunca zero.
4. **`days_to_first_order` pode ser negativo** se a primeira compra antecede o
   `customer_since` do cadastro. Isso não é corrigido: é um achado
   (`dq.customer.signup_after_first_order`) e revela ordem de cadastro invertida
   na origem.
5. **`avg_days_between_orders` é `(last - first) / (n-1)`**, e exige `n >= 2`.
   Não é a média dos intervalos reais — é a aproximação linear, e está declarada
   como tal porque a diferença importa para quem modela churn.
6. **`cohort_month` vem de `customer_since`** (cadastro), não da primeira compra.
   A coorte por primeira compra é outra métrica; se for necessária, é um mart novo.
7. **`retention_rate` tem como denominador o tamanho da coorte inteira**, não os
   ativos do mês anterior. É retenção absoluta, não mês a mês.
8. **`preferred_channel`** é o canal com mais pedidos (`topKWeighted(1)`). Empate é
   resolvido arbitrariamente e de forma **não estável entre refreshes** — quem
   precisar de estabilidade deve usar `distinct_channels` e decidir no consumo.
9. **RFM não é materializado como score.** Os scores 1-5 dependem dos quantis da
   população, que mudam a cada refresh; materializá-los faria o score de um cliente
   mudar sem que nada dele mudasse. O mart entrega R, F e M brutos, e o score é
   calculado no consumo (ver § Consumo).
10. **`is_active_90d` usa `now()`**, então muda entre refreshes mesmo sem pedido
    novo. É intencional e é o motivo de o refresh ser de 30 min e não diário.
11. **Cliente apagado no SAP** (`_is_deleted = 1`) desaparece do mart, e seus
    pedidos passam a ser órfãos — contabilizados no cenário 009, não silenciados.

---

## Armadilhas

### 1. Janela de dias
Ver [§ Por que não há janela](#por-que-não-há-janela). É o erro mais provável aqui,
porque todos os outros marts usam janela.

### 2. `INNER JOIN` a partir do pedido
`FROM order INNER JOIN customer` perde quem nunca comprou, e a retenção fica
inflada (denominador menor). O `FROM` é o cliente, e o join é `LEFT`.

### 3. `count()` no lugar de `countDistinct()`
Com o `LEFT JOIN` de itens já colapsado por `order_id`, `count()` ainda contaria
uma linha por pedido — o que aqui coincide. Mas basta alguém remover a CTE para o
número de pedidos virar número de itens. `countDistinct(order_id)` é explícito
sobre a intenção.

### 4. Agregador incremental
Pedido é mutável e cancelamento reescreve o passado. Armadilha 4 do
`CLAUDE.md` §8, na sua forma mais severa (sem janela que salve).

### 5. `recency_days = 0` para quem nunca comprou
Faria todo cliente inativo parecer o mais engajado da base. `Nullable` resolve, e
o filtro de segmentação precisa usar `is_active_90d` em vez de `recency_days <= 90`
(que é nulo-falso, o que por acaso funciona — mas por acidente, não por desenho).

### 6. Projection duplicando custo de escrita
Uma projection de tabela larga dobra o volume escrito no refresh total. É o
trade-off do ADR-0011 §2, e a duração do refresh com e sem projection deve entrar
em `docs/evaluation/results.md`.

### 7. `topKWeighted` como fonte de verdade
Não é determinístico em empate. Documentado na regra 8 para não virar bug de
"o canal preferido do cliente mudou sem motivo".

---

## Critérios de aceite

- [ ] **Todo cliente aparece, inclusive sem pedido**:
  ```sql
  SELECT (SELECT count() FROM dh_marts.v_customer_metrics)
       - (SELECT count() FROM dh_core.v_customer__customer_current) AS diff;
  -- esperado: 0
  SELECT total_orders, never_purchased, recency_days
  FROM dh_marts.v_customer_metrics WHERE customer_id = 'CUST-SEM-PEDIDO';
  -- esperado: 0, 1, NULL
  ```
- [ ] **Cancelamento retroativo corrige o lifetime** (o teste que valida a ausência
  de janela): cliente com pedido de 200 dias atrás; cancelar via CDC; aguardar
  refresh.
  ```sql
  SELECT total_orders, orders_canceled, lifetime_net
  FROM dh_marts.v_customer_metrics WHERE customer_id = 'CUST-CANCEL-ANTIGO';
  -- esperado: lifetime_net reduzido em 200,00 e orders_canceled = 1
  ```
- [ ] **Dedup de pedido**: pedido com 5 transições de status.
  ```sql
  SELECT total_orders FROM dh_marts.v_customer_metrics WHERE customer_id = 'CUST-5-STATUS';
  -- esperado: 1  (não 5)
  ```
- [ ] **Lifetime fecha com L1**:
  ```sql
  SELECT abs(m.net - d.net) AS diff FROM
   (SELECT sum(lifetime_net) net FROM dh_marts.v_customer_metrics) m,
   (SELECT sum(total_amount) net FROM dh_core.v_sales__order_current
    WHERE order_status != 'canceled'
      AND customer_id IN (SELECT customer_id FROM dh_core.v_customer__customer_current)) d;
  -- esperado: 0
  ```
- [ ] **`avg_days_between_orders` nulo com 1 pedido**:
  ```sql
  SELECT avg_days_between_orders, is_one_time_buyer
  FROM dh_marts.v_customer_metrics WHERE customer_id = 'CUST-1-PEDIDO';
  -- esperado: NULL, 1
  ```
- [ ] **Coorte: retenção do mês 0 não excede 100%**:
  ```sql
  SELECT count() FROM dh_marts.customer_cohort_month FINAL
  WHERE retention_rate > 1.0 OR retention_rate < 0 OR isNaN(retention_rate);
  -- esperado: 0
  ```
- [ ] **Coorte: `cohort_size` é estável entre meses de atividade**:
  ```sql
  SELECT count() FROM (SELECT cohort_month, segment, countDistinct(cohort_size) c
    FROM dh_marts.customer_cohort_month FINAL GROUP BY 1,2 HAVING c > 1);
  -- esperado: 0
  ```
- [ ] **Cadastro após primeira compra detectado**:
  ```sql
  SELECT count() FROM dh_marts.v_customer_metrics WHERE days_to_first_order < 0;
  -- esperado: aparece no check dq.customer.signup_after_first_order, não é zerado
  ```
- [ ] **Leitura pontual rápida** (perfil aplicacional, ADR-0011):
  ```sql
  SELECT * FROM dh_marts.v_customer_metrics WHERE customer_id = 'CUST-000001';
  -- esperado: < 50 ms (p95), medido em make bench
  ```
- [ ] **Projection usada na varredura por coorte**: `EXPLAIN indexes = 1` da query
  de segmentação mostra `p_by_cohort_segment`.
- [ ] **Duração do refresh total registrada** em `docs/evaluation/results.md`, com
  o número de clientes do dataset de benchmark.

---

## Checks de qualidade associados

| check_id | Regra | Severidade |
|---|---|---|
| `dq.marts.sum_parity` | `sum(lifetime_net)` == soma em L1 dos pedidos de clientes existentes | error |
| `dq.marts.row_parity` | **novo** — linhas do mart == clientes em `v_customer__customer_current` | error |
| `dq.customer.cohort_bounds` | **novo** — `retention_rate` em [0,1], `cohort_size` estável | error |
| `dq.customer.signup_after_first_order` | **novo** — `days_to_first_order < 0` em menos de 0,5% | warn |
| `dq.marts.refresh_health` | refresh sem exceção, `last_success` < 90 min | error |
| `dq.marts.refresh_duration` | **novo** — duração do refresh total < 50% do intervalo | warn |

`dq.marts.refresh_duration` é específico deste mart: refresh total que passa a
durar mais que o intervalo é o sinal antecipado de que a estratégia sem janela
deixou de escalar.

---

## Consumo

### Segmentação RFM (score calculado no consumo, regra 9)

```sql
WITH base AS (
    SELECT customer_id, segment, loyalty_tier,
           recency_days, total_orders, lifetime_net
    FROM dh_marts.v_customer_metrics
    WHERE never_purchased = 0
)
SELECT customer_id, segment,
       6 - ntile(5) OVER (ORDER BY recency_days ASC)  AS r_score,  -- menor recência = 5
           ntile(5) OVER (ORDER BY total_orders ASC)  AS f_score,
           ntile(5) OVER (ORDER BY lifetime_net ASC)  AS m_score
FROM base;
```

### Curva de retenção por coorte

```sql
SELECT cohort_month, months_since_signup, any(cohort_size) AS size,
       sum(active_customers) AS active, sum(active_customers) / any(cohort_size) AS retention
FROM dh_marts.customer_cohort_month FINAL
WHERE cohort_month >= toStartOfMonth(today() - 365) AND segment = 'b2c'
GROUP BY cohort_month, months_since_signup
ORDER BY cohort_month, months_since_signup;
```

### Endpoint aplicacional

`GET /customers/{customer_id}/metrics` — leitura pontual em
`dh_marts.v_customer_metrics`, role `dh_app`, frescor de até 30 min declarado na
resposta (`X-Data-Freshness`). A tela de atendimento que precisa do pedido
**agora** usa o cenário 001, não este.

---

## Arquivos no repositório

| Caminho | Conteúdo |
|---|---|
| `internal/contexts/analytics/sql/30-marts/0060__customer_metrics.sql` | tabela, projection, Refreshable MV, view |
| `internal/contexts/analytics/sql/30-marts/0061__customer_cohort_month.sql` | tabela e Refreshable MV da coorte |
| `internal/contexts/analytics/sql/40-reports/0060__customer.sql` | views de segmentação e curva de retenção |
| `internal/contexts/analytics/sql/40-reports/0061__dq_customer.sql` | `dq.marts.row_parity`, `dq.customer.*` |
| `internal/contexts/analytics/reports/customer_metrics.go` | queries nomeadas |
| `internal/reporting/http/customer_handler.go` | `GET /customers/{id}/metrics` |
| `internal/contexts/customer/generator/customer.go` | gerador com cliente sem pedido, cadastro após primeira compra, cliente apagado |
| `test/e2e/customer_metrics_test.go` | cancelamento retroativo, cliente sem pedido, dedup, coorte |
