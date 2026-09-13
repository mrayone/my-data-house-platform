# Cenário 004 — Efetividade de cupom e campanha

- **Perfil:** analítico
- **Pergunta de negócio:** "Cada campanha trouxe ticket maior ou só deu desconto? Quantos usos cada cupom teve contra o limite? E que cupons estão sendo aceitos sem existir no cadastro?"
- **Mecanismo:** **Dictionary** para o cadastro de cupons + **Refreshable MV** para a agregação (ADR-0005)
- **Frescor esperado:** ≤ 16 min (`REFRESH EVERY 15 MINUTE`)
- **Janela de recálculo:** substituição total (o mart é pequeno — milhares de cupons)
- **Entidades envolvidas:** `sales.order`, `sales.order_item`, `pricing.discount_codes` (dictionary), `organization.business_unit` (dictionary)
- **Tipo de correlação:** **por código (string)**, não por ID surrogate — e a **ausência** de correspondência é um dos resultados
- **ADRs relevantes:** ADR-0001, ADR-0004 (§4, §6), ADR-0005 (§3), ADR-0010, ADR-0011

---

## Por que este cenário existe na PoC

1. **Prova a correlação por valor de negócio, não por chave técnica.**
   `order.discount_code` é uma string digitada pelo cliente. Não há FK, não há
   integridade referencial na origem, e a normalização (caixa, espaço) é
   responsabilidade de quem consulta. É o caso mais frágil de correlação do
   cenário real, e por isso precisa estar na PoC.
2. **Prova que a ausência de correspondência é informação, não erro.** Cupom
   aceito pelo checkout que não existe no cadastro do SAP é um achado de negócio
   (campanha criada fora do SAP, cupom expirado ainda aceito, fraude) — e a
   plataforma tem de **entregar isso como relatório**, não descartar a linha nem
   falhar a ingestão.
3. **Prova comparação entre coortes dentro do mesmo mart.** Ticket médio com
   cupom vs sem cupom exige um baseline na mesma janela e na mesma BU — é onde a
   comparação ingênua ("ticket com cupom é maior, logo cupom funciona") quebra.

---

## Fontes

| Tabela | Objeto lido | Camada | Por que este e não outro |
|---|---|---|---|
| Pedido | `dh_core.v_sales__order_current` | L1 | Estado atual. O `discount_code` do pedido pode mudar (cliente troca o cupom antes de pagar); só a última versão conta. |
| Itens | `dh_core.v_sales__order_item_current` | L1 | Para `units_sold`. Agregado por `order_id` antes do join. |
| Cadastro de cupom | `dh_core.dict__discount_codes` | L2 | Milhares de linhas, lookup por chave exata. `dictHas` é o que responde "existe no cadastro?" em `O(1)`, sem anti-join. |
| Unidade de negócio | `dh_core.dict__business_unit` | L2 | Contextualiza a campanha por canal. |

---

## Modelo de saída

Duas tabelas, porque são dois grãos diferentes e juntá-las produziria linha com
semântica ambígua.

```sql
-- internal/contexts/analytics/sql/30-marts/0040__discount_effectiveness.sql

-- (1) grão: cupom × dia × BU
CREATE TABLE IF NOT EXISTS dh_marts.discount_effectiveness {ON_CLUSTER}
(
    order_date             Date,
    discount_code_norm     String,        -- normalizado (upper + trim)
    discount_code_raw      String,        -- como veio, para rastreio
    business_unit_code     LowCardinality(String),

    -- cadastro (vazio quando o cupom não existe)
    code_found             UInt8,
    campaign_name          String,
    discount_type          LowCardinality(String),
    discount_value         Decimal(18,4),
    valid_from             Nullable(DateTime64(3)),
    valid_to               Nullable(DateTime64(3)),
    max_uses               Int64,
    bu_scope               String,

    -- classificação do uso
    used_outside_validity  UInt64,        -- pedidos fora da janela de vigência
    used_outside_bu_scope  UInt64,        -- pedidos em BU fora do escopo do cupom

    -- uso e resultado
    orders                 UInt64,
    orders_canceled        UInt64,
    units_sold             Int64,
    gross_amount           Decimal(38,4),
    discount_amount        Decimal(38,4),
    net_amount             Decimal(38,4),
    avg_ticket             Decimal(38,4),
    discount_pct_effective Float64,       -- discount_amount / gross_amount

    _refreshed_at          DateTime64(3)
)
ENGINE = ReplacingMergeTree(_refreshed_at)
PARTITION BY toYYYYMM(order_date)
ORDER BY (discount_code_norm, order_date, business_unit_code);

-- (2) grão: BU × dia — baseline sem cupom, para comparação honesta
CREATE TABLE IF NOT EXISTS dh_marts.discount_baseline_day {ON_CLUSTER}
(
    order_date            Date,
    business_unit_code    LowCardinality(String),
    orders_no_coupon      UInt64,
    net_no_coupon         Decimal(38,4),
    avg_ticket_no_coupon  Decimal(38,4),
    orders_with_coupon    UInt64,
    net_with_coupon       Decimal(38,4),
    avg_ticket_with_coupon Decimal(38,4),
    _refreshed_at         DateTime64(3)
)
ENGINE = ReplacingMergeTree(_refreshed_at)
PARTITION BY toYYYYMM(order_date)
ORDER BY (business_unit_code, order_date);
```

Decisões do DDL:

- **`discount_code_norm` e `discount_code_raw` separados.** O agrupamento usa o
  normalizado; o cru fica para rastrear de onde veio `" promo10"` com espaço.
  Perder o cru impediria diagnosticar problema de checkout.
- **`ORDER BY (discount_code_norm, order_date, ...)`** na tabela 1: o acesso
  dominante é "como foi o cupom X ao longo do tempo".
- **`max_uses` como `Int64` com `0` quando não cadastrado**, não `Nullable`:
  simplifica a comparação `orders > max_uses` sem tratamento de nulo, e
  `code_found = 0` já diz que o valor não é confiável.
- **`used_outside_validity` e `used_outside_bu_scope` como contadores**, não
  flags: no mesmo dia um cupom pode ter usos válidos e inválidos.

---

## Transformação

```sql
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_marts.mv__discount_effectiveness {ON_CLUSTER}
REFRESH EVERY 15 MINUTE APPEND
TO dh_marts.discount_effectiveness AS
WITH
    (today() - 90) AS window_from,

    order_items AS (
        SELECT order_id, sum(quantity) AS units
        FROM dh_core.v_sales__order_item_current
        WHERE toDate(created_at) >= window_from
        GROUP BY order_id
    ),

    -- normalização acontece UMA vez, aqui, e é reusada
    orders_with_code AS (
        SELECT
            o.order_id,
            toDate(o.created_at)                        AS order_date,
            o.created_at                                AS created_at,
            o.business_unit_code                        AS business_unit_code,
            o.order_status                              AS order_status,
            o.gross_amount                              AS gross_amount,
            o.discount_amount                           AS discount_amount,
            o.total_amount                              AS total_amount,
            o.discount_code                             AS discount_code_raw,
            upperUTF8(trimBoth(o.discount_code))        AS discount_code_norm
        FROM dh_core.v_sales__order_current AS o
        WHERE toDate(o.created_at) >= window_from
          AND o.discount_code IS NOT NULL
          AND trimBoth(o.discount_code) != ''
    )
SELECT
    c.order_date                                                  AS order_date,
    c.discount_code_norm                                          AS discount_code_norm,
    any(c.discount_code_raw)                                      AS discount_code_raw,
    c.business_unit_code                                          AS business_unit_code,

    -- dictHas responde "existe no cadastro?" sem anti-join
    dictHas('dh_core.dict__discount_codes', tuple(c.discount_code_norm)) AS code_found,
    dictGetOrDefault('dh_core.dict__discount_codes', 'campaign_name',
                     tuple(c.discount_code_norm), '(sem cadastro)') AS campaign_name,
    dictGetOrDefault('dh_core.dict__discount_codes', 'discount_type',
                     tuple(c.discount_code_norm), 'unknown')       AS discount_type,
    dictGetOrDefault('dh_core.dict__discount_codes', 'discount_value',
                     tuple(c.discount_code_norm), toDecimal64(0,4)) AS discount_value,
    dictGetOrNull('dh_core.dict__discount_codes', 'valid_from',
                  tuple(c.discount_code_norm))                     AS valid_from,
    dictGetOrNull('dh_core.dict__discount_codes', 'valid_to',
                  tuple(c.discount_code_norm))                     AS valid_to,
    dictGetOrDefault('dh_core.dict__discount_codes', 'max_uses',
                     tuple(c.discount_code_norm), toInt64(0))      AS max_uses,
    dictGetOrDefault('dh_core.dict__discount_codes', 'business_unit_scope',
                     tuple(c.discount_code_norm), '')              AS bu_scope,

    -- uso fora da vigência: só avaliável se o cupom existe no cadastro
    countIf(
        dictHas('dh_core.dict__discount_codes', tuple(c.discount_code_norm))
        AND ( c.created_at < dictGetOrNull('dh_core.dict__discount_codes','valid_from', tuple(c.discount_code_norm))
           OR c.created_at > dictGetOrNull('dh_core.dict__discount_codes','valid_to',   tuple(c.discount_code_norm)) )
    )                                                              AS used_outside_validity,

    -- uso fora do escopo de BU: bu_scope vazio significa "todas as BUs"
    countIf(
        dictHas('dh_core.dict__discount_codes', tuple(c.discount_code_norm))
        AND dictGetOrDefault('dh_core.dict__discount_codes','business_unit_scope',
                             tuple(c.discount_code_norm), '') != ''
        AND dictGetOrDefault('dh_core.dict__discount_codes','business_unit_scope',
                             tuple(c.discount_code_norm), '') != c.business_unit_code
    )                                                              AS used_outside_bu_scope,

    countDistinct(c.order_id)                                      AS orders,
    countDistinctIf(c.order_id, c.order_status = 'canceled')       AS orders_canceled,
    sum(coalesce(oi.units, 0))                                     AS units_sold,

    sumIf(c.gross_amount,    c.order_status != 'canceled')         AS gross_amount,
    sumIf(c.discount_amount, c.order_status != 'canceled')         AS discount_amount,
    sumIf(c.total_amount,    c.order_status != 'canceled')         AS net_amount,

    if(countDistinctIf(c.order_id, c.order_status != 'canceled') = 0, toDecimal128(0,4),
       sumIf(c.total_amount, c.order_status != 'canceled')
         / countDistinctIf(c.order_id, c.order_status != 'canceled')) AS avg_ticket,

    if(sumIf(c.gross_amount, c.order_status != 'canceled') = 0, 0,
       toFloat64(sumIf(c.discount_amount, c.order_status != 'canceled'))
         / toFloat64(sumIf(c.gross_amount, c.order_status != 'canceled'))) AS discount_pct_effective,

    now64(3)                                                       AS _refreshed_at

FROM orders_with_code AS c
LEFT JOIN order_items AS oi USING (order_id)
GROUP BY order_date, discount_code_norm, business_unit_code, code_found,
         campaign_name, discount_type, discount_value, valid_from, valid_to,
         max_uses, bu_scope;
```

```sql
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_marts.mv__discount_baseline_day {ON_CLUSTER}
REFRESH EVERY 15 MINUTE APPEND
TO dh_marts.discount_baseline_day AS
WITH (today() - 90) AS window_from,
     -- um pedido "tem cupom" quando discount_code não é nulo nem vazio
     has_coupon AS (discount_code IS NOT NULL AND trimBoth(discount_code) != '')
SELECT
    toDate(created_at)                                        AS order_date,
    business_unit_code                                        AS business_unit_code,
    countDistinctIf(order_id, NOT has_coupon AND order_status != 'canceled') AS orders_no_coupon,
    sumIf(total_amount,       NOT has_coupon AND order_status != 'canceled') AS net_no_coupon,
    if(countDistinctIf(order_id, NOT has_coupon AND order_status != 'canceled') = 0, toDecimal128(0,4),
       sumIf(total_amount,    NOT has_coupon AND order_status != 'canceled')
         / countDistinctIf(order_id, NOT has_coupon AND order_status != 'canceled')) AS avg_ticket_no_coupon,
    countDistinctIf(order_id, has_coupon AND order_status != 'canceled')     AS orders_with_coupon,
    sumIf(total_amount,       has_coupon AND order_status != 'canceled')     AS net_with_coupon,
    if(countDistinctIf(order_id, has_coupon AND order_status != 'canceled') = 0, toDecimal128(0,4),
       sumIf(total_amount,    has_coupon AND order_status != 'canceled')
         / countDistinctIf(order_id, has_coupon AND order_status != 'canceled')) AS avg_ticket_with_coupon,
    now64(3)                                                  AS _refreshed_at
FROM dh_core.v_sales__order_current
WHERE toDate(created_at) >= window_from
GROUP BY order_date, business_unit_code;
```

Views de leitura e o relatório de cupons não cadastrados:

```sql
CREATE VIEW IF NOT EXISTS dh_marts.v_discount_effectiveness {ON_CLUSTER} AS
SELECT * EXCEPT (_refreshed_at) FROM dh_marts.discount_effectiveness FINAL;

-- internal/contexts/analytics/sql/40-reports/0040__discount.sql
CREATE VIEW IF NOT EXISTS dh_reports.v_unknown_discount_codes {ON_CLUSTER} AS
SELECT discount_code_norm, any(discount_code_raw) AS example_raw,
       min(order_date) AS first_seen, max(order_date) AS last_seen,
       sum(orders) AS orders, sum(discount_amount) AS discount_given
FROM dh_marts.v_discount_effectiveness
WHERE code_found = 0
GROUP BY discount_code_norm
ORDER BY discount_given DESC;

CREATE VIEW IF NOT EXISTS dh_reports.v_discount_over_limit {ON_CLUSTER} AS
SELECT discount_code_norm, campaign_name, max_uses,
       sum(orders) AS total_uses, sum(orders) - max_uses AS over_by
FROM dh_marts.v_discount_effectiveness
WHERE code_found = 1 AND max_uses > 0
GROUP BY discount_code_norm, campaign_name, max_uses
HAVING total_uses > max_uses
ORDER BY over_by DESC;
```

---

## Regras de negócio

1. **Grão da tabela 1:** cupom normalizado × dia × BU. Um pedido usa no máximo um
   cupom (restrição da origem); cupom cumulativo está fora de escopo.
2. **Normalização é `upperUTF8(trimBoth(code))`** e nada mais. Não se remove
   acento, hífen nem espaço interno — isso mudaria o código e mascararia um
   problema de checkout. A normalização escolhida é a mínima que corrige os dois
   erros observados (caixa e espaço nas pontas).
3. **Pedido sem cupom não entra na tabela 1.** Ele é o baseline, e vive na
   tabela 2.
4. **Cupom não cadastrado não é descartado.** Entra com `code_found = 0`,
   `campaign_name = '(sem cadastro)'`, e é o conteúdo de
   `dh_reports.v_unknown_discount_codes`.
5. **Pedido cancelado** conta em `orders` e `orders_canceled`, mas não em nenhum
   valor monetário. Cupom "usado" em pedido cancelado ainda consumiu uso — por
   isso `orders` inclui cancelados, e a comparação com `max_uses` também.
6. **Vigência e escopo de BU só são avaliados quando `code_found = 1`.** Sem
   cadastro, não há vigência contra a qual comparar; os contadores ficam em zero e
   isso não significa "válido".
7. **`used_outside_validity` usa `order.created_at`**, não a data de pagamento: a
   validade do cupom se afere no momento da compra.
8. **`bu_scope` vazio significa "válido para todas as BUs"** — não "sem escopo
   definido". Essa é a semântica da origem, e está aqui explícita porque é
   exatamente o tipo de convenção que se perde.
9. **`discount_pct_effective` é `discount_amount / gross_amount`** realizado, não
   o `discount_value` cadastrado. A diferença entre os dois é o achado: cupom de
   10% que resulta em 18% de desconto efetivo indica regra aplicada em cima de
   outra.
10. **Comparação com o baseline não é causal.** O mart entrega os dois números
    lado a lado; afirmar que o cupom *causou* o ticket maior exigiria desenho
    experimental. Está escrito aqui para que o relatório não seja lido como prova
    de causalidade.

---

## Armadilhas

### 1. Agrupar pelo código cru
`"PROMO10"`, `"promo10"` e `" PROMO10"` viram três campanhas distintas, e cada uma
parece ter um terço do uso real. Por isso a normalização acontece na CTE, antes de
qualquer agrupamento.

### 2. `INNER JOIN` com o cadastro de cupons
Seria o reflexo natural — e apagaria silenciosamente exatamente o achado mais
valioso do cenário. `dictHas` + `dictGetOrDefault` preserva a linha e marca a
ausência.

### 3. `dictGetOrDefault` com default que parece dado real
Default de `campaign_name` é `'(sem cadastro)'`, não `''` nem o próprio código.
Um default ambíguo aparece no painel como se fosse uma campanha.

### 4. Comparar ticket com cupom vs sem cupom sem controlar BU e janela
Cupom concentrado em BU de ticket naturalmente alto "prova" que cupom aumenta
ticket. O baseline é por BU **e** por dia por isso.

### 5. Média de `discount_pct_effective`
É uma razão: a média das razões não é a razão das somas. O consumo (§ Consumo)
recalcula de `discount_amount / gross_amount`.

### 6. Agregador incremental
`order.discount_code` é mutável — o cliente troca o cupom antes de pagar. Agregado
incremental contaria o pedido nos dois cupons. Armadilha 4 do `CLAUDE.md` §8.

### 7. Dictionary defasado gerando falso "não cadastrado"
`LIFETIME(MIN 300 MAX 600)` significa até 10 min de defasagem. Campanha criada
agora aparece como não cadastrada por alguns minutos. O relatório de cupons
desconhecidos filtra por `last_seen < now() - 15 min` para não gerar alarme falso.

---

## Critérios de aceite

- [ ] **Normalização agrupa corretamente**: produzir pedidos com `"PROMO10"`,
  `"promo10"` e `" PROMO10 "`.
  ```sql
  SELECT discount_code_norm, sum(orders) FROM dh_marts.v_discount_effectiveness
  WHERE discount_code_norm = 'PROMO10' GROUP BY 1;
  -- esperado: uma única linha, orders = 3
  ```
- [ ] **Cupom não cadastrado aparece e não quebra nada**:
  ```sql
  SELECT orders, code_found, campaign_name FROM dh_marts.v_discount_effectiveness
  WHERE discount_code_norm = 'CUPOM-FANTASMA';
  -- esperado: code_found = 0, campaign_name = '(sem cadastro)', orders >= 1
  SELECT count() FROM dh_reports.v_unknown_discount_codes
  WHERE discount_code_norm = 'CUPOM-FANTASMA';
  -- esperado: 1
  ```
- [ ] **Uso fora da vigência detectado**: cadastrar cupom com `valid_to` ontem e
  produzir pedido hoje.
  ```sql
  SELECT used_outside_validity FROM dh_marts.v_discount_effectiveness
  WHERE discount_code_norm = 'CUPOM-EXPIRADO' AND order_date = today();
  -- esperado: >= 1
  ```
- [ ] **Uso fora do escopo de BU detectado**: cupom com `business_unit_scope='BU-01'`
  usado em `BU-02`.
  ```sql
  -- esperado: used_outside_bu_scope >= 1
  ```
- [ ] **Estouro de `max_uses` detectado**: cupom com `max_uses = 2` usado 3 vezes.
  ```sql
  SELECT over_by FROM dh_reports.v_discount_over_limit
  WHERE discount_code_norm = 'CUPOM-LIMITE';
  -- esperado: 1
  ```
- [ ] **Troca de cupom não duplica**: produzir pedido com `PROMO10` (`_cdc_seq=10`)
  e depois `PROMO20` (`_cdc_seq=20`).
  ```sql
  SELECT sum(orders) FROM dh_marts.v_discount_effectiveness
  WHERE order_date = today() AND discount_code_norm IN ('PROMO10','PROMO20');
  -- esperado: 1  (só PROMO20; o pedido não conta nos dois)
  ```
- [ ] **Baseline fecha com o total**:
  ```sql
  SELECT (SELECT sum(orders_no_coupon + orders_with_coupon)
          FROM dh_marts.discount_baseline_day FINAL WHERE order_date = today())
       - (SELECT countDistinct(order_id) FROM dh_core.v_sales__order_current
          WHERE toDate(created_at) = today() AND order_status != 'canceled') AS diff;
  -- esperado: 0
  ```
- [ ] **Dictionary de cupons carregado** e **refresh saudável**.

---

## Checks de qualidade associados

| check_id | Regra | Severidade |
|---|---|---|
| `dq.rel.orphan_rate` (`order → discount_codes`) | cupom sem cadastro < 5% dos pedidos com cupom (threshold alto **de propósito** — é o achado) | warn |
| `dq.marts.sum_parity` | baseline fecha com contagem de pedidos em L1 | error |
| `dq.dict.loaded` | `dict__discount_codes` `LOADED` | error |
| `dq.marts.refresh_health` | refresh sem exceção | error |
| `dq.discount.over_limit` | **novo** — nenhum cupom com uso > `max_uses` | warn |
| `dq.discount.outside_validity` | **novo** — uso fora da vigência < 0,1% | warn |

---

## Consumo

```sql
-- efetividade por campanha nos últimos 30 dias, contra o baseline da BU
SELECT d.campaign_name,
       sum(d.orders)                                   AS orders,
       sum(d.discount_amount)                          AS discount_given,
       sum(d.discount_amount) / sum(d.gross_amount)    AS pct_effective,   -- razão das somas
       sum(d.net_amount)      / sum(d.orders)          AS avg_ticket_coupon,
       sum(b.net_no_coupon)   / sum(b.orders_no_coupon) AS avg_ticket_baseline
FROM dh_marts.v_discount_effectiveness AS d
INNER JOIN dh_marts.discount_baseline_day AS b FINAL
       ON b.order_date = d.order_date AND b.business_unit_code = d.business_unit_code
WHERE d.order_date >= today() - 30 AND d.code_found = 1
GROUP BY d.campaign_name
ORDER BY discount_given DESC;
```

Relatórios operacionais prontos: `dh_reports.v_unknown_discount_codes` (para o
time de marketing e o de risco) e `dh_reports.v_discount_over_limit`.

Sem endpoint aplicacional — é consumo analítico (role `dh_analyst`).

---

## Arquivos no repositório

| Caminho | Conteúdo |
|---|---|
| `internal/contexts/analytics/sql/30-marts/0040__discount_effectiveness.sql` | as duas tabelas, as duas Refreshable MVs, as views |
| `internal/contexts/analytics/sql/40-reports/0040__discount.sql` | `v_unknown_discount_codes`, `v_discount_over_limit` |
| `internal/contexts/analytics/sql/40-reports/0041__dq_discount.sql` | `dq.discount.over_limit`, `dq.discount.outside_validity` |
| `internal/contexts/pricing/sql/30-marts/0020__dict_discount_codes.sql` | `dh_core.dict__discount_codes` |
| `internal/contexts/analytics/reports/discount_effectiveness.go` | query nomeada |
| `internal/contexts/pricing/generator/discount.go` | gerador com cupom fantasma, expirado, fora de escopo e acima do limite |
| `test/e2e/discount_effectiveness_test.go` | normalização, cupom fantasma, troca de cupom, estouro de limite |
