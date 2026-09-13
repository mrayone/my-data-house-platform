# Cenário 003 — Funil e taxa de aprovação de pagamento

- **Perfil:** analítico (com consumo aplicacional do funil do pedido via cenário 001)
- **Pergunta de negócio:** "Qual a taxa de aprovação por método, adquirente e faixa de parcelas, e quanto tempo leva até a autorização?"
- **Mecanismo:** **Refreshable MV** com janela de 3 dias (ADR-0005: `order_payment` é entidade mutável — o status de captura muda ao longo do tempo)
- **Frescor esperado:** ≤ 6 min (`REFRESH EVERY 5 MINUTE`)
- **Janela de recálculo:** 3 dias
- **Entidades envolvidas:** `sales.order_payment`, `sales.order`, `organization.business_unit` (dictionary)
- **Tipo de correlação:** **por ID, fan-out 1:N** (`order_payment.order_id` → `order.order_id`)
- **ADRs relevantes:** ADR-0001, ADR-0004 (§4, §6), ADR-0005 (§2, §3), ADR-0010, ADR-0011

---

## Por que este cenário existe na PoC

1. **Prova o fan-out 1:N.** Um pedido tem N tentativas de pagamento. Todo o resto
   da PoC agrega **para** o pedido; aqui o grão é a tentativa, e o pedido é a
   dimensão. É a direção oposta, e ela expõe um erro diferente: contar pedidos
   com `count()` em vez de `countDistinct()`.
2. **Prova que transição de estado se mede sem event sourcing.** O CDC não entrega
   "eventos de transição" — entrega o estado atual da tentativa, várias vezes. O
   funil (`pending → authorized → captured`) é reconstruído do **estado final** de
   cada tentativa, não de uma sequência de eventos. É uma limitação real do CDC que
   a PoC precisa expor, não esconder.
3. **Prova cálculo de latência a partir de colunas nuláveis.**
   `authorized_at` é nulo enquanto não autorizado — e `quantile` sobre nulo é uma
   fonte clássica de número errado.

---

## Fontes

| Tabela | Objeto lido | Camada | Por que este e não outro |
|---|---|---|---|
| Tentativas | `dh_core.v_sales__order_payment_current` | L1 | Estado **final** de cada tentativa, deduplicado por `_cdc_seq`. Ler a tabela core direto contaria cada transição de status como uma tentativa distinta — inflando o denominador da taxa de aprovação. |
| Pedido | `dh_core.v_sales__order_current` | L1 | Fornece `business_unit_code`, `channel` e `currency`. `INNER JOIN`: tentativa sem pedido é órfã e vai para o cenário 009, não para o funil. |
| Unidade de negócio | `dh_core.dict__business_unit` | L2 | Lookup por chave; ver cenário 002. |

---

## Modelo de saída

```sql
-- internal/contexts/analytics/sql/30-marts/0030__payment_funnel_day.sql

CREATE TABLE IF NOT EXISTS dh_marts.payment_funnel_day {ON_CLUSTER}
(
    attempt_date           Date,
    business_unit_code     LowCardinality(String),
    bu_name                String,
    order_channel          LowCardinality(String),
    payment_method         LowCardinality(String),
    acquirer               LowCardinality(String),
    installment_bucket     LowCardinality(String),   -- '1' | '2-6' | '7-12' | '13+'
    currency               LowCardinality(String),

    -- funil (grão = tentativa)
    attempts               UInt64,
    attempts_pending       UInt64,
    attempts_authorized    UInt64,
    attempts_captured      UInt64,
    attempts_denied        UInt64,
    attempts_refunded      UInt64,

    -- pedidos (grão = pedido distinto; NÃO somável entre linhas)
    orders_with_attempt    UInt64,
    orders_captured        UInt64,

    -- valores
    amount_attempted       Decimal(38,4),
    amount_captured        Decimal(38,4),
    amount_refunded        Decimal(38,4),

    -- taxas (materializadas para o BI não recalcular errado)
    approval_rate          Float64,      -- authorized+captured / attempts
    capture_rate           Float64,      -- captured / (authorized+captured)
    denial_rate            Float64,

    -- latência até autorização, em segundos (só de tentativas autorizadas)
    auth_latency_p50_s     Float64,
    auth_latency_p95_s     Float64,
    auth_latency_p99_s     Float64,
    auth_latency_max_s     Float64,

    -- retentativa
    max_attempts_per_order UInt32,
    orders_with_retry      UInt64,

    _refreshed_at          DateTime64(3)
)
ENGINE = ReplacingMergeTree(_refreshed_at)
PARTITION BY toYYYYMM(attempt_date)
ORDER BY (attempt_date, payment_method, acquirer, installment_bucket,
          business_unit_code, currency);
```

Decisões do DDL:

- **`ORDER BY` começa em `attempt_date`** (e não em BU como no cenário 002):
  o filtro dominante aqui é temporal — "como está a aprovação hoje vs ontem".
  Método e adquirente vêm em seguida porque são os cortes seguintes mais comuns.
- **`installment_bucket` como string de faixa**, não `installments` cru: reduz a
  cardinalidade de ~24 valores para 4 e é como o negócio pensa o dado. O valor
  cru continua em L1 para quem precisar.
- **Taxas materializadas como `Float64`**: são razões, não dinheiro — `Float64` é
  correto aqui e `dq.money.no_float` só olha colunas monetárias.
- **`orders_with_attempt` e `orders_captured` não são somáveis** entre linhas
  (o mesmo pedido pode ter tentativas em métodos diferentes). Documentado na
  regra 7 e exposto como aviso no doc do endpoint.

---

## Transformação

```sql
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_marts.mv__payment_funnel_day {ON_CLUSTER}
REFRESH EVERY 5 MINUTE APPEND
TO dh_marts.payment_funnel_day AS
WITH
    (today() - 3) AS window_from,

    -- número de tentativas por pedido, para medir retentativa
    attempts_per_order AS (
        SELECT order_id, count() AS n
        FROM dh_core.v_sales__order_payment_current
        WHERE toDate(created_at) >= window_from
        GROUP BY order_id
    )
SELECT
    toDate(p.created_at)                                          AS attempt_date,
    o.business_unit_code                                          AS business_unit_code,
    dictGetOrDefault('dh_core.dict__business_unit', 'bu_name',
                     tuple(o.business_unit_code), '(sem cadastro)') AS bu_name,
    o.channel                                                     AS order_channel,
    p.payment_method                                              AS payment_method,
    p.acquirer                                                    AS acquirer,
    multiIf(p.installments <= 1,  '1',
            p.installments <= 6,  '2-6',
            p.installments <= 12, '7-12',
                                  '13+')                          AS installment_bucket,
    o.currency                                                    AS currency,

    count()                                                       AS attempts,
    countIf(p.payment_status = 'pending')                         AS attempts_pending,
    countIf(p.payment_status = 'authorized')                      AS attempts_authorized,
    countIf(p.payment_status = 'captured')                         AS attempts_captured,
    countIf(p.payment_status = 'denied')                          AS attempts_denied,
    countIf(p.payment_status = 'refunded')                        AS attempts_refunded,

    -- countDistinct e NÃO count: o grão da linha é a tentativa
    countDistinct(p.order_id)                                     AS orders_with_attempt,
    countDistinctIf(p.order_id, p.payment_status = 'captured')    AS orders_captured,

    sum(p.amount)                                                 AS amount_attempted,
    sumIf(p.amount, p.payment_status = 'captured')                AS amount_captured,
    sumIf(p.amount, p.payment_status = 'refunded')                AS amount_refunded,

    -- taxas: denominador explícito, divisão por zero vira 0 e nunca NaN
    if(count() = 0, 0,
       countIf(p.payment_status IN ('authorized','captured')) / count())      AS approval_rate,
    if(countIf(p.payment_status IN ('authorized','captured')) = 0, 0,
       countIf(p.payment_status = 'captured')
         / countIf(p.payment_status IN ('authorized','captured')))            AS capture_rate,
    if(count() = 0, 0, countIf(p.payment_status = 'denied') / count())        AS denial_rate,

    -- latência: SÓ de tentativas com authorized_at preenchido.
    -- quantileIf ignora as linhas que não satisfazem a condição; sem o If,
    -- authorized_at nulo entraria como 0 e derrubaria os quantis.
    quantileIf(0.50)(dateDiff('second', p.created_at, p.authorized_at),
                     p.authorized_at IS NOT NULL)                 AS auth_latency_p50_s,
    quantileIf(0.95)(dateDiff('second', p.created_at, p.authorized_at),
                     p.authorized_at IS NOT NULL)                 AS auth_latency_p95_s,
    quantileIf(0.99)(dateDiff('second', p.created_at, p.authorized_at),
                     p.authorized_at IS NOT NULL)                 AS auth_latency_p99_s,
    maxIf(dateDiff('second', p.created_at, p.authorized_at),
          p.authorized_at IS NOT NULL)                            AS auth_latency_max_s,

    max(coalesce(apo.n, 1))                                       AS max_attempts_per_order,
    countDistinctIf(p.order_id, coalesce(apo.n, 1) > 1)           AS orders_with_retry,

    now64(3)                                                      AS _refreshed_at

FROM dh_core.v_sales__order_payment_current AS p
-- INNER JOIN: tentativa sem pedido não entra no funil (vai para o cenário 009)
INNER JOIN dh_core.v_sales__order_current AS o USING (order_id)
LEFT  JOIN attempts_per_order AS apo USING (order_id)
WHERE toDate(p.created_at) >= window_from
GROUP BY attempt_date, business_unit_code, bu_name, order_channel,
         payment_method, acquirer, installment_bucket, currency;
```

```sql
CREATE VIEW IF NOT EXISTS dh_marts.v_payment_funnel_day {ON_CLUSTER} AS
SELECT * EXCEPT (_refreshed_at) FROM dh_marts.payment_funnel_day FINAL;
```

---

## Regras de negócio

1. **Grão da linha é a tentativa de pagamento**, agrupada por dia, método,
   adquirente, faixa de parcelas, BU e moeda.
2. **`attempt_date` é `toDate(payment.created_at)`** — a data em que a tentativa
   foi criada, não a de autorização. Uma tentativa criada às 23h58 e autorizada
   às 00h02 conta no dia da criação; é o que permite fechar o funil por dia.
3. **Estado considerado é o final da tentativa**, não a trajetória. O CDC não
   entrega transições, e a PoC não reconstrói o que a origem não fornece.
   Consequência assumida: uma tentativa que passou por `pending → denied →
   authorized` (retry no mesmo `payment_id`) aparece só como `authorized`.
4. **Aprovada = `authorized` OU `captured`.** `captured` implica que passou por
   `authorized`, então somar as duas no numerador não duplica — são estados
   mutuamente exclusivos no snapshot.
5. **`refunded` não reduz `amount_captured`.** São colunas separadas, porque o
   estorno costuma acontecer em outro dia e subtrair reescreveria o passado.
   Receita líquida de estorno é conta do consumidor.
6. **Latência só de tentativas autorizadas.** `authorized_at` nulo é excluído do
   cálculo — nunca tratado como zero.
7. **`orders_with_attempt`, `orders_captured`, `orders_with_retry` não são
   somáveis entre linhas.** O mesmo pedido pode aparecer em duas linhas (duas
   tentativas, métodos diferentes). Somar essas colunas conta o pedido duas vezes.
8. **Tentativa sem pedido não entra** (`INNER JOIN`). Isso é deliberado e medido:
   a contagem de tentativas excluídas é um check (`dq.rel.orphan_rate` da relação
   `order_payment → order`).
9. **Valor negativo em `amount`** é inconsistência da origem: não é filtrado, e
   aparece como achado em `dq.money.no_negative_amount`.
10. **Moeda no grão**, nunca somada entre moedas (mesma regra do cenário 002).

---

## Armadilhas

### 1. `count()` onde deveria ser `countDistinct()`
O grão é a tentativa. `count()` sobre pedidos daria "pedidos = tentativas", e a
taxa de aprovação por pedido sairia errada em toda BU com muita retentativa.

### 2. `quantile` sobre `Nullable` sem filtro
`dateDiff('second', created_at, NULL)` não é nulo em todos os contextos — e
agregação sobre nulo tem comportamento diferente por função. Usar `quantileIf`
com `IS NOT NULL` explícito é o que garante que o p95 seja o p95 **das
autorizadas**, e não de uma população contaminada por zeros.

### 3. Ler `dh_core.sales__order_payment` sem dedup
Uma tentativa que passa por 3 status gera 3 linhas em L0. Sem `FINAL`, `attempts`
triplica e `approval_rate` fica diluída. Bloqueado por `no-direct-core-read.sh`.

### 4. Agregador incremental
`order_payment` é mutável por definição (o status é o que muda). Agregado
incremental somaria a tentativa em `pending` **e** em `captured`. Armadilha 4 do
`CLAUDE.md` §8.

### 5. `installments = 0` na origem
O SAP às vezes envia `0` para pagamento à vista. O `multiIf` usa `<= 1` em vez de
`= 1` justamente por isso.

### 6. Confundir taxa de aprovação de tentativa com de pedido
São duas métricas diferentes e frequentemente confundidas em reunião.
`approval_rate` é por **tentativa**. A taxa por **pedido** é
`orders_captured / orders_with_attempt`, e só é válida na própria linha.
O doc do endpoint precisa dizer qual está mostrando.

---

## Critérios de aceite

- [ ] **Tentativas fecham com L1**:
  ```sql
  SELECT (SELECT sum(attempts) FROM dh_marts.v_payment_funnel_day
          WHERE attempt_date >= today() - 3)
       - (SELECT count() FROM dh_core.v_sales__order_payment_current p
          INNER JOIN dh_core.v_sales__order_current o USING (order_id)
          WHERE toDate(p.created_at) >= today() - 3) AS diff;
  -- esperado: 0
  ```
- [ ] **Dedup efetiva**: produzir a mesma tentativa em `pending`, `authorized` e
  `captured` (3 versões, `_cdc_seq` crescente).
  ```sql
  SELECT attempts, attempts_captured FROM dh_marts.v_payment_funnel_day
  WHERE acquirer = 'ACQ-TEST' AND attempt_date = today();
  -- esperado: attempts = 1, attempts_captured = 1  (não attempts = 3)
  ```
- [ ] **Taxas no intervalo válido**:
  ```sql
  SELECT count() FROM dh_marts.v_payment_funnel_day
  WHERE approval_rate NOT BETWEEN 0 AND 1 OR capture_rate NOT BETWEEN 0 AND 1
     OR isNaN(approval_rate) OR isNaN(capture_rate);
  -- esperado: 0
  ```
- [ ] **Latência não contaminada por nulo**: produzir 9 tentativas autorizadas em
  10 s e 1 não autorizada.
  ```sql
  SELECT auth_latency_p50_s FROM dh_marts.v_payment_funnel_day
  WHERE acquirer = 'ACQ-LAT-TEST' AND attempt_date = today();
  -- esperado: ~10 (não ~0, que seria o efeito do nulo virando zero)
  ```
- [ ] **Fan-out preservado**: pedido com 3 tentativas no mesmo método.
  ```sql
  SELECT attempts, orders_with_attempt, max_attempts_per_order, orders_with_retry
  FROM dh_marts.v_payment_funnel_day WHERE acquirer = 'ACQ-RETRY-TEST';
  -- esperado: attempts = 3, orders_with_attempt = 1,
  --           max_attempts_per_order = 3, orders_with_retry = 1
  ```
- [ ] **Órfãos medidos, não escondidos**: produzir tentativa sem pedido.
  ```sql
  SELECT value, passed FROM dh_meta.dq_check_results
  WHERE check_id = 'dq.rel.orphan_rate.order_payment__order'
  ORDER BY run_at DESC LIMIT 1;
  -- esperado: a tentativa aparece no valor do check, e NÃO no mart
  ```
- [ ] **Refresh saudável** e **dictionary carregado** (mesmas queries do cenário 002).

---

## Checks de qualidade associados

| check_id | Regra | Severidade |
|---|---|---|
| `dq.marts.sum_parity` | `sum(attempts)` do mart == contagem em L1 (com pedido) | error |
| `dq.marts.refresh_health` | refresh sem exceção, `last_success` < 15 min | error |
| `dq.rel.orphan_rate` | tentativa sem pedido < 0,5% | error |
| `dq.marts.rate_bounds` | **novo** — toda taxa em [0,1] e não `NaN` | error |
| `dq.money.no_negative_amount` | **novo** — `amount < 0` em menos de 0,01% das tentativas | warn |
| `dq.dict.loaded` | `dict__business_unit` `LOADED` | error |

---

## Consumo

```sql
-- aprovação por adquirente e faixa de parcelas nos últimos 7 dias
SELECT acquirer, installment_bucket,
       sum(attempts)                                          AS attempts,
       sum(attempts_authorized + attempts_captured) / sum(attempts) AS approval_rate,
       max(auth_latency_p95_s)                                AS worst_p95_s
FROM dh_marts.v_payment_funnel_day
WHERE attempt_date >= today() - 7
GROUP BY acquirer, installment_bucket
ORDER BY attempts DESC;
```

Note que a taxa é **recalculada** a partir de numerador e denominador, não pela
média de `approval_rate` — média de razões é errada. O endpoint e o painel do BI
devem seguir a mesma regra.

Endpoint aplicacional: não há um para o funil agregado. O status de pagamento de
um pedido específico vem do cenário 001 (`GET /orders/{order_id}/360`).

---

## Arquivos no repositório

| Caminho | Conteúdo |
|---|---|
| `internal/contexts/analytics/sql/30-marts/0030__payment_funnel_day.sql` | tabela, Refreshable MV, view |
| `internal/contexts/analytics/sql/40-reports/0030__payment_funnel.sql` | views de L3 |
| `internal/contexts/analytics/sql/40-reports/0031__dq_payment.sql` | `dq.marts.rate_bounds`, `dq.money.no_negative_amount` |
| `internal/contexts/analytics/reports/payment_funnel.go` | query nomeada |
| `internal/contexts/sales/generator/payment.go` | gerador com retentativa, negação e autorização tardia |
| `test/e2e/payment_funnel_test.go` | dedup, fan-out, latência com nulo, órfão |
