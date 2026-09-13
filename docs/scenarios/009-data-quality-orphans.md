# Cenário 009 — Observabilidade de dado: orfandade, atraso e integridade referencial

- **Perfil:** analítico e operacional. **Este relatório é um entregável da PoC, não instrumentação interna.**
- **Pergunta de negócio:** "Posso confiar nos outros nove relatórios? O que está faltando, desde quando, e isso é atraso normal ou incidente?"
- **Mecanismo:** **Refreshable MV** (`REFRESH EVERY 5 MINUTE`) + checks SQL versionados gravados em `dh_meta.dq_check_results` (ADR-0010)
- **Frescor esperado:** ≤ 6 min
- **Janela de recálculo:** 7 dias para orfandade; snapshot completo para freshness e reconciliação
- **Entidades envolvidas:** **todas** — mais as tabelas de L0 e `system.*`
- **Tipo de correlação:** **ausência de correlação** (anti-join) + metadados de ingestão
- **ADRs relevantes:** ADR-0004 (§7 fora de ordem), ADR-0010 (integral), ADR-0002 (§DLQ), ADR-0005

---

## Por que este cenário existe na PoC

Os erros deste desenho são **plausíveis**. Uma receita 3% menor porque
`order_item` está órfão não parece errada. Um funil de pagamento com taxa de
aprovação alta porque as tentativas negadas ainda não chegaram não parece errado.

Sem este cenário, a PoC pode "funcionar" e levar a uma decisão de contratação
baseada em números falsos. É o relatório que torna "funcionou" uma afirmação
verificável.

Ele endereça três riscos distintos:

1. **Confundir atraso com ausência.** Por
   [ADR-0004 §7](../adr/0004-semantica-cdc-dedup-ordem-delete.md), item que chega
   antes do pedido é **normal**. O que se mede é **taxa** e **idade** — não
   presença. Órfão de 30 segundos é o sistema funcionando; órfão de 6 horas é
   incidente.
2. **Perda silenciosa entre L0 e L1.** A MV incremental pode falhar por
   incompatibilidade de tipo sem que nada apareça no connector. A reconciliação de
   chaves distintas L0↔L1 é o que detecta.
3. **Tópico que parou.** Um connector pausado não gera erro — gera silêncio. Tópico
   de cadastro pode ficar dias legitimamente sem mensagem; tópico transacional, não.
   O SLA por contrato (`source.freshness_sla_minutes`) é o que separa os dois.

---

## Fontes

| Objeto lido | Camada | Para quê |
|---|---|---|
| `dh_landing.<ctx>__<entidade>_raw` | L0 | freshness (`max(_kafka_ts)`), contagem de chaves distintas, monotonicidade de `_cdc_seq`, distribuição de `_op` |
| `dh_core.v_<ctx>__<entidade>_current` | L1 | reconciliação de chaves, base dos anti-joins |
| `system.view_refreshes` | — | saúde das Refreshable MVs |
| `system.dictionaries` | — | status e memória dos dictionaries |
| `system.parts` | — | contagem de parts e volume por tabela |
| `dh_meta.dq_checks` / `dq_check_results` | — | catálogo e histórico dos checks |
| Tópico de DLQ do Connect | — | profundidade da DLQ por connector (coletado por `dhctl`, não por SQL) |

Os anti-joins usam as views `v_*_current` de ambos os lados: órfão que só existe
porque a tabela core não mergeou seria um falso positivo.

---

## Modelo de saída

Três objetos, com grãos deliberadamente diferentes.

```sql
-- internal/contexts/analytics/sql/30-marts/0070__data_quality_overview.sql

-- (1) painel por entidade: uma linha por entidade, snapshot do estado
CREATE TABLE IF NOT EXISTS dh_marts.data_quality_overview {ON_CLUSTER}
(
    context                 LowCardinality(String),
    entity                  LowCardinality(String),
    topic                   LowCardinality(String),

    -- liveness
    last_kafka_ts           Nullable(DateTime64(3)),
    freshness_seconds       Nullable(Int64),
    freshness_sla_seconds   Int64,
    freshness_ok            UInt8,

    -- volume
    landing_rows            UInt64,
    landing_distinct_keys   UInt64,
    core_rows               UInt64,          -- linhas na view corrente (deduplicadas)
    core_deleted_keys       UInt64,

    -- reconciliação L0 <-> L1
    reconcile_diff          Int64,           -- landing_distinct_keys - (core_rows + core_deleted_keys)
    reconcile_ok            UInt8,

    -- CDC
    ops_create              UInt64,
    ops_update              UInt64,
    ops_delete              UInt64,
    ops_snapshot            UInt64,
    keys_with_dup_in_core   UInt64,          -- deve ser sempre 0
    cdc_seq_non_monotonic   UInt64,

    -- armazenamento
    active_parts            UInt64,
    bytes_on_disk           UInt64,

    _refreshed_at           DateTime64(3)
)
ENGINE = ReplacingMergeTree(_refreshed_at)
ORDER BY (context, entity);

-- (2) orfandade por relação declarada no contrato: taxa E idade
CREATE TABLE IF NOT EXISTS dh_marts.orphan_summary {ON_CLUSTER}
(
    relation_id          LowCardinality(String),   -- 'sales.order_item -> sales.order'
    from_context         LowCardinality(String),
    from_entity          LowCardinality(String),
    to_context           LowCardinality(String),
    to_entity            LowCardinality(String),
    relation_kind        LowCardinality(String),   -- foreign_key | lookup | temporal | loose

    total_rows           UInt64,
    orphan_rows          UInt64,
    orphan_rate_pct      Float64,
    threshold_pct        Float64,
    rate_ok              UInt8,

    -- a métrica que separa atraso de incidente
    orphan_age_p50_s     Nullable(Int64),
    orphan_age_p95_s     Nullable(Int64),
    orphan_age_p99_s     Nullable(Int64),
    orphan_age_max_s     Nullable(Int64),
    refresh_window_s     Int64,                    -- janela do mart que consome a relação
    age_ok               UInt8,                    -- p99 < refresh_window_s

    _refreshed_at        DateTime64(3)
)
ENGINE = ReplacingMergeTree(_refreshed_at)
ORDER BY (relation_id);

-- (3) órfãos individuais, para investigação (não é agregado)
CREATE TABLE IF NOT EXISTS dh_marts.orphan_records {ON_CLUSTER}
(
    relation_id      LowCardinality(String),
    orphan_key       String,          -- chave concatenada do lado "from"
    missing_key      String,          -- chave que não foi encontrada no lado "to"
    first_seen_at    DateTime64(3),   -- _ingested_at da linha órfã
    age_seconds      Int64,
    sample_topic     LowCardinality(String),
    sample_partition UInt16,
    sample_offset    UInt64,
    _refreshed_at    DateTime64(3)
)
ENGINE = ReplacingMergeTree(_refreshed_at)
PARTITION BY toYYYYMMDD(first_seen_at)
ORDER BY (relation_id, orphan_key)
TTL toDateTime(first_seen_at) + INTERVAL 14 DAY;
```

Decisões do DDL:

- **Três tabelas, não uma.** Painel por entidade, resumo por relação e registros
  individuais têm grãos incompatíveis; juntá-los produziria linha de semântica
  ambígua — o erro que este cenário existe para não cometer.
- **`refresh_window_s` materializado em `orphan_summary`**: o critério de "órfão
  aceitável" é *menor que a janela do mart que consome a relação*. Sem essa coluna,
  quem lê o painel não sabe contra o que comparar.
- **`orphan_records` com TTL de 14 dias** e partição diária: é dado de
  investigação, não histórico. Sem TTL, cresce sem limite em incidente.
- **`reconcile_diff` como `Int64` assinado**: diferença negativa (mais chaves em L1
  que em L0) significa que o TTL de L0 expirou antes — diagnóstico diferente de
  perda na MV, e o sinal é o que distingue.
- **`keys_with_dup_in_core` sempre 0**: qualquer valor diferente é falha da
  premissa do ADR-0004, severidade `error`.

---

## Transformação

O SQL é **gerado** a partir dos contratos: `dhctl generate` produz um bloco
`SELECT` por entidade e por relação declarada, unidos por `UNION ALL`. Escrever
isso à mão para 30 entidades seria inconsistência garantida
([ADR-0003](../adr/0003-extensibilidade-contract-first.md)) — e é o que torna este
cenário extensível junto com o resto.

Forma do bloco gerado por entidade (exemplo para `sales.order`):

```sql
-- GENERATED by dhctl from contracts/domains/**/*.yaml — DO NOT EDIT
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_marts.mv__data_quality_overview {ON_CLUSTER}
REFRESH EVERY 5 MINUTE APPEND
TO dh_marts.data_quality_overview AS
SELECT
    'sales'                      AS context,
    'order'                      AS entity,
    'sap.sales.order.v1'         AS topic,
    l.last_kafka_ts              AS last_kafka_ts,
    l.freshness_seconds          AS freshness_seconds,
    600                          AS freshness_sla_seconds,   -- do contrato
    if(l.freshness_seconds IS NULL, 0, l.freshness_seconds <= 600) AS freshness_ok,
    l.landing_rows, l.landing_distinct_keys,
    c.core_rows, c.core_deleted_keys,
    toInt64(l.landing_distinct_keys) - toInt64(c.core_rows + c.core_deleted_keys) AS reconcile_diff,
    if(abs(toInt64(l.landing_distinct_keys) - toInt64(c.core_rows + c.core_deleted_keys)) = 0, 1, 0) AS reconcile_ok,
    l.ops_create, l.ops_update, l.ops_delete, l.ops_snapshot,
    c.keys_with_dup_in_core,
    l.cdc_seq_non_monotonic,
    p.active_parts, p.bytes_on_disk,
    now64(3)                     AS _refreshed_at
FROM
    ( SELECT
          max(_kafka_ts)                                        AS last_kafka_ts,
          dateDiff('second', max(_kafka_ts), now())             AS freshness_seconds,
          count()                                               AS landing_rows,
          countDistinct(order_id)                               AS landing_distinct_keys,
          countIf(_op = 'c')                                    AS ops_create,
          countIf(_op = 'u')                                    AS ops_update,
          countIf(_op = 'd')                                    AS ops_delete,
          countIf(_op = 'r')                                    AS ops_snapshot,
          -- chave cujo _cdc_seq máximo não corresponde ao maior _offset:
          -- indício de chegada fora de ordem dentro do tópico
          countDistinctIf(order_id, 1) - countDistinctIf(order_id, 1) AS cdc_seq_non_monotonic
      FROM dh_landing.sales__order_raw ) AS l,
    ( SELECT
          count()                                               AS core_rows,
          0                                                     AS core_deleted_keys,
          countDistinct(order_id) - count()                     AS keys_with_dup_in_core
      FROM dh_core.v_sales__order_current ) AS c,
    ( SELECT count() AS active_parts, sum(bytes_on_disk) AS bytes_on_disk
      FROM system.parts
      WHERE database = 'dh_landing' AND table = 'sales__order_raw' AND active ) AS p
UNION ALL
-- ... um bloco por entidade
;
```

> **Nota para o executor da Fase 08:** `core_deleted_keys` e
> `cdc_seq_non_monotonic` exigem subquery sobre a tabela core **sem** `FINAL`
> (para ver as linhas `_is_deleted = 1`) e sobre L0 com `argMax`. Os dois blocos
> estão especificados em `internal/codegen/ddl/dq_overview.go.tmpl` e são a única
> exceção documentada à regra "nunca ler a tabela core direto" — porque aqui o
> objeto de medição **é** o estado físico. A exceção é anotada com
> `-- dq-exception: reads core table intentionally` e o check
> `no-direct-core-read.sh` ignora linhas com esse marcador.

Orfandade, forma do bloco gerado por relação:

```sql
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_marts.mv__orphan_summary {ON_CLUSTER}
REFRESH EVERY 5 MINUTE APPEND
TO dh_marts.orphan_summary AS
WITH (today() - 7) AS window_from
SELECT
    'sales.order_item -> sales.order'  AS relation_id,
    'sales' AS from_context, 'order_item' AS from_entity,
    'sales' AS to_context,   'order'      AS to_entity,
    'foreign_key'                      AS relation_kind,
    count()                            AS total_rows,
    countIf(o.order_id = '')           AS orphan_rows,
    if(count() = 0, 0, 100.0 * countIf(o.order_id = '') / count()) AS orphan_rate_pct,
    0.5                                AS threshold_pct,      -- do contrato
    if(count() = 0, 1,
       100.0 * countIf(o.order_id = '') / count() <= 0.5)     AS rate_ok,
    quantileIf(0.50)(dateDiff('second', i._ingested_at, now()), o.order_id = '') AS orphan_age_p50_s,
    quantileIf(0.95)(dateDiff('second', i._ingested_at, now()), o.order_id = '') AS orphan_age_p95_s,
    quantileIf(0.99)(dateDiff('second', i._ingested_at, now()), o.order_id = '') AS orphan_age_p99_s,
    maxIf(dateDiff('second', i._ingested_at, now()), o.order_id = '')            AS orphan_age_max_s,
    259200                             AS refresh_window_s,   -- 3 dias (janela do order_360)
    if(quantileIf(0.99)(dateDiff('second', i._ingested_at, now()), o.order_id = '') IS NULL, 1,
       quantileIf(0.99)(dateDiff('second', i._ingested_at, now()), o.order_id = '') < 259200) AS age_ok,
    now64(3)                           AS _refreshed_at
-- LEFT JOIN + coluna vazia é o anti-join: preserva o total no denominador
FROM dh_core.v_sales__order_item_current AS i
LEFT JOIN dh_core.v_sales__order_current AS o USING (order_id)
WHERE toDate(i.created_at) >= window_from
UNION ALL
-- ... um bloco por relação declarada nos contratos
;
```

Views de leitura:

```sql
CREATE VIEW IF NOT EXISTS dh_marts.v_data_quality_overview {ON_CLUSTER} AS
SELECT * EXCEPT (_refreshed_at) FROM dh_marts.data_quality_overview FINAL;

CREATE VIEW IF NOT EXISTS dh_marts.v_orphan_summary {ON_CLUSTER} AS
SELECT * EXCEPT (_refreshed_at) FROM dh_marts.orphan_summary FINAL;

-- internal/contexts/analytics/sql/40-reports/0070__data_quality.sql
CREATE VIEW IF NOT EXISTS dh_reports.v_dq_alerts {ON_CLUSTER} AS
SELECT 'freshness' AS kind, context, entity,
       toString(freshness_seconds) AS detail
FROM dh_marts.v_data_quality_overview WHERE freshness_ok = 0
UNION ALL
SELECT 'reconcile', context, entity, toString(reconcile_diff)
FROM dh_marts.v_data_quality_overview WHERE reconcile_ok = 0
UNION ALL
SELECT 'core_dup', context, entity, toString(keys_with_dup_in_core)
FROM dh_marts.v_data_quality_overview WHERE keys_with_dup_in_core != 0
UNION ALL
SELECT 'orphan_rate', from_context, from_entity,
       concat(relation_id, ' = ', toString(orphan_rate_pct), '%')
FROM dh_marts.v_orphan_summary WHERE rate_ok = 0
UNION ALL
SELECT 'orphan_age', from_context, from_entity,
       concat(relation_id, ' p99 = ', toString(orphan_age_p99_s), 's')
FROM dh_marts.v_orphan_summary WHERE age_ok = 0;
```

---

## Regras de negócio

1. **Órfão transitório é funcionamento normal, não erro.** A métrica é **taxa** e
   **idade**, nunca presença. Um relatório que alarmasse com o primeiro órfão seria
   inútil em 30 segundos de operação.
2. **O critério de órfão aceitável é `orphan_age_p99_s < refresh_window_s`.**
   Órfão mais novo que a janela do mart que o consome ainda vai ser corrigido pelo
   próximo refresh. Mais velho, não vai — e aí é incidente.
3. **Cada relação tem seu próprio threshold de taxa**, vindo de
   `relations[].orphan_threshold_pct` no contrato. `order → discount_codes` tem 5%
   **de propósito** (cupom sem cadastro é o achado do cenário 004);
   `order_item → order` tem 0,5%.
4. **A idade do órfão é medida por `_ingested_at`**, não `_kafka_ts` nem
   `created_at`: o que interessa é quanto tempo o dado está **na plataforma** sem
   par, não quanto tempo tem o fato na origem. Um pedido de 2023 que chega hoje é
   órfão novo.
5. **Reconciliação compara chaves distintas**, não linhas: L0 tem N versões por
   chave e L1 tem uma. `landing_distinct_keys` deve igualar
   `core_rows + core_deleted_keys`.
6. **`reconcile_diff` negativo tem diagnóstico próprio:** mais chaves em L1 que em
   L0 significa que o TTL de L0 expirou. Não é perda de dado — é perda de
   capacidade de reprocesso, que é grave de outra forma.
7. **`freshness_sla_seconds` vem do contrato** (`source.freshness_sla_minutes`).
   Cadastro pode ter SLA de 24h; transacional tem 10 min. Sem SLA por entidade,
   ou o cadastro alarma sempre ou o transacional nunca.
8. **`keys_with_dup_in_core != 0` é sempre `error`.** É violação da premissa do
   ADR-0004, e todo relatório do sistema fica suspeito.
9. **Entidade sem nenhuma mensagem** tem `freshness_seconds = NULL` e
   `freshness_ok = 0`. Nulo não é "ok por falta de dado".
10. **Este mart nunca corrige nada.** Ele mede. Correção é ação humana ou de
    runbook, e o runbook é referenciado, não executado.
11. **`orphan_records` é amostra para investigação**, limitada a 100 mil linhas por
    relação por refresh. O número exato de órfãos está em `orphan_summary`; a
    listagem é para achar o padrão.

---

## Armadilhas

### 1. `INNER JOIN` no anti-join
`FROM order_item INNER JOIN order` retorna apenas os **não** órfãos. O anti-join
precisa de `LEFT JOIN` e do teste de coluna vazia — e o denominador
(`total_rows`) tem de ser o total, senão a taxa é sempre 0%.

### 2. `NULL` vs `''` no `LEFT JOIN` do ClickHouse
Coluna não-`Nullable` de tabela à direita vem como **valor padrão do tipo** (`''`
para `String`, `0` para números), **não** `NULL`. `WHERE o.order_id IS NULL` não
funciona aqui e retornaria zero órfãos — silenciosamente. O teste é
`o.order_id = ''`. Esta é a armadilha mais perigosa deste cenário.

### 3. Anti-join sem janela temporal
Sem `WHERE created_at >= today() - 7`, a taxa de órfãos é diluída por todo o
histórico e nunca cruza o threshold, mesmo com o pipeline quebrado hoje.

### 4. Medir presença em vez de idade
"Existe órfão" é verdadeiro praticamente sempre. Alarmar nisso treina o time a
ignorar o alerta — que é pior que não ter alerta.

### 5. Ler a tabela core sem `FINAL` por acidente
Duplicata não mergeada apareceria como órfão (a chave existe, mas o join encontra
versões inconsistentes). As duas pontas do anti-join usam `v_*_current`. As duas
exceções deliberadas (contar `_is_deleted` e medir monotonicidade) estão anotadas
com `-- dq-exception:` e são as únicas.

### 6. Confiar em `dh_marts` para medir `dh_marts`
Se a Refreshable MV deste cenário falha, o painel congela com os últimos valores —
e "tudo verde" passa a significar "não sei". Por isso `dq.marts.refresh_health`
lê `system.view_refreshes` diretamente e o `dhctl dq run` roda **fora** do mart.
O painel é a apresentação; a autoridade é `dh_meta.dq_check_results`.

### 7. DLQ medida por SQL
A profundidade da DLQ vive no Kafka, não no ClickHouse. `dhctl dq run` consulta o
Connect/Kafka e **grava** o resultado em `dh_meta.dq_check_results`. Tentar medir
isso em SQL puro produziria um check que nunca falha.

---

## Critérios de aceite

- [ ] **Órfão transitório aparece e desaparece sozinho** (o teste central):
  produzir `order_item` do pedido `ORD-ORPHAN` **sem** o pedido; aguardar refresh;
  depois produzir o pedido; aguardar refresh.
  ```sql
  -- após o 1º refresh:
  SELECT orphan_rows, orphan_age_max_s, age_ok FROM dh_marts.v_orphan_summary
  WHERE relation_id = 'sales.order_item -> sales.order';
  -- esperado: orphan_rows >= 1, age_ok = 1 (novo, dentro da janela)

  -- após o pedido chegar e o 2º refresh:
  -- esperado: orphan_rows volta ao valor anterior, sem intervenção
  ```
- [ ] **Órfão envelhecido dispara `age_ok = 0`**: injetar órfão com
  `_ingested_at` de 4 dias atrás (janela é 3).
  ```sql
  -- esperado: age_ok = 0 e linha em dh_reports.v_dq_alerts com kind='orphan_age'
  ```
- [ ] **O anti-join realmente conta órfãos** (teste da armadilha 2):
  ```sql
  SELECT orphan_rows FROM dh_marts.v_orphan_summary
  WHERE relation_id = 'sales.order_item -> sales.order';
  -- esperado: > 0 quando há órfão conhecido. Se for sempre 0, o teste
  -- IS NULL foi usado no lugar de = '' — revisar antes de seguir.
  ```
- [ ] **Reconciliação detecta perda na MV**: dropar temporariamente a MV
  `mv__sales__order_raw__to__order`, produzir 10 pedidos, recriar.
  ```sql
  SELECT reconcile_diff, reconcile_ok FROM dh_marts.v_data_quality_overview
  WHERE context='sales' AND entity='order';
  -- esperado: reconcile_diff = 10, reconcile_ok = 0
  ```
- [ ] **Duplicata em core é detectada**: `keys_with_dup_in_core`.
  ```sql
  SELECT count() FROM dh_marts.v_data_quality_overview WHERE keys_with_dup_in_core != 0;
  -- esperado: 0 em operação normal; > 0 se alguém alterar o ORDER BY da core
  ```
- [ ] **Freshness respeita o SLA por entidade**: pausar o connector de
  `business_unit` (SLA 24h) e o de `order` (SLA 10 min) por 20 min.
  ```sql
  SELECT entity, freshness_ok FROM dh_marts.v_data_quality_overview
  WHERE entity IN ('order','business_unit');
  -- esperado: order -> 0, business_unit -> 1
  ```
- [ ] **Entidade sem mensagem não passa por padrão**:
  ```sql
  SELECT freshness_seconds, freshness_ok FROM dh_marts.v_data_quality_overview
  WHERE entity = 'entidade-nunca-produzida';
  -- esperado: NULL, 0
  ```
- [ ] **Todo contrato aparece no painel** (o painel é gerado, logo é completo):
  ```sql
  SELECT count() FROM dh_marts.v_data_quality_overview;
  -- esperado: igual ao número de contratos em contracts/domains/**/*.yaml
  ```
- [ ] **Toda relação declarada aparece no resumo de orfandade**:
  ```sql
  SELECT count() FROM dh_marts.v_orphan_summary;
  -- esperado: igual ao número total de entradas `relations` nos contratos
  ```
- [ ] **`dhctl dq run --severity error` sai 0** com o pipeline saudável e **não-0**
  com um connector pausado.
- [ ] **`v_dq_alerts` vazia** em operação normal.

---

## Checks de qualidade associados

Este cenário **é** a implementação do catálogo do
[ADR-0010](../adr/0010-observabilidade-e-qualidade-de-dados.md):

| check_id | Fonte | Severidade |
|---|---|---|
| `dq.landing.freshness` | `data_quality_overview.freshness_ok` | error |
| `dq.landing.dlq_depth` | Connect/Kafka via `dhctl` | error |
| `dq.core.dup_key` | `keys_with_dup_in_core` | error |
| `dq.core.cdc_monotonic` | `cdc_seq_non_monotonic` | warn |
| `dq.core.reconcile_count` | `reconcile_ok` | error |
| `dq.core.deleted_leak` | query direta na view corrente | error |
| `dq.rel.orphan_rate` | `orphan_summary.rate_ok` (por relação) | error |
| `dq.rel.orphan_age_p99` | `orphan_summary.age_ok` (por relação) | error |
| `dq.rel.fk_violation` | `orphan_summary` de relações `foreign_key` | warn |
| `dq.marts.refresh_health` | `system.view_refreshes` (direto) | error |
| `dq.dict.loaded` | `system.dictionaries` (direto) | error |
| `dq.money.no_float` | `system.columns` | error |

Os quatro últimos leem `system.*` **diretamente**, não pelo mart — pela razão da
armadilha 6.

---

## Consumo

### Painel operacional (o "está tudo bem?")

```sql
SELECT * FROM dh_reports.v_dq_alerts;
-- vazio = saudável. Qualquer linha é ação.
```

### Estado por entidade

```sql
SELECT context, entity,
       freshness_seconds, freshness_sla_seconds,
       landing_distinct_keys, core_rows, reconcile_diff,
       formatReadableSize(bytes_on_disk) AS size, active_parts
FROM dh_marts.v_data_quality_overview
ORDER BY freshness_ok ASC, context, entity;
```

### Orfandade: atraso ou incidente?

```sql
SELECT relation_id, relation_kind,
       orphan_rows, round(orphan_rate_pct, 3) AS rate_pct, threshold_pct,
       orphan_age_p99_s, refresh_window_s,
       multiIf(rate_ok = 0 AND age_ok = 0, 'INCIDENTE',
               age_ok = 0,                 'ATRASO ACIMA DA JANELA',
               rate_ok = 0,                'TAXA ALTA',
                                           'normal') AS veredito
FROM dh_marts.v_orphan_summary
ORDER BY rate_ok, age_ok, orphan_rate_pct DESC;
```

### Histórico dos checks (a autoridade)

```sql
SELECT check_id, run_at, value, passed
FROM dh_meta.dq_check_results
WHERE run_at > now() - INTERVAL 24 HOUR AND passed = 0
ORDER BY run_at DESC;
```

Endpoint: `GET /health/data` retorna `200` com `v_dq_alerts` vazia e `503` com
qualquer alerta de severidade `error`. É o que um sistema a jusante consulta antes
de confiar nos números.

---

## Arquivos no repositório

| Caminho | Conteúdo |
|---|---|
| `internal/contexts/analytics/sql/30-marts/0070__data_quality_overview.sql` | **gerado** — painel por entidade |
| `internal/contexts/analytics/sql/30-marts/0071__orphan_summary.sql` | **gerado** — resumo por relação |
| `internal/contexts/analytics/sql/30-marts/0072__orphan_records.sql` | tabela de amostra + MVs geradas |
| `internal/contexts/analytics/sql/40-reports/0070__data_quality.sql` | `v_dq_alerts` e views de painel |
| `internal/codegen/ddl/dq_overview.go` + `.tmpl` | gerador dos blocos por entidade e por relação |
| `db/shared/00-bootstrap/0030__dh_meta.sql` | `dq_checks`, `dq_check_results`, `pipeline_health` |
| `internal/platform/observability/dq.go` | runner de `dhctl dq run`, incluindo DLQ e `system.*` |
| `cmd/dhctl/dq.go` | subcomando `dq run`, `dq list` |
| `internal/reporting/http/health_handler.go` | `GET /health/data` |
| `scripts/checks/no-direct-core-read.sh` | respeita o marcador `-- dq-exception:` |
| `test/e2e/data_quality_test.go` | órfão transitório, órfão envelhecido, reconciliação, freshness por SLA |
