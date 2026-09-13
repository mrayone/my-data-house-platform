# Cenário 002 — Receita por unidade de negócio e dia

- **Perfil:** analítico
- **Pergunta de negócio:** "Qual a receita bruta e líquida por unidade de negócio, canal e dia, nos últimos 90 dias?"
- **Mecanismo:** **Refreshable MV** com janela de 3 dias + **Dictionary** para a dimensão de BU (ADR-0005, árvore de decisão: agregação sobre entidade **mutável** → nunca agregador incremental)
- **Frescor esperado:** ≤ 16 min (refresh de 15 min + duração)
- **Janela de recálculo:** 3 dias
- **Entidades envolvidas:** `sales.order`, `sales.order_item`, `organization.business_unit` (dictionary)
- **Tipo de correlação:** **por coluna não-ID** (`order.business_unit_code` → `business_unit.bu_code`), resolvida por `dictGet`
- **ADRs relevantes:** ADR-0001, ADR-0004 (§4, §6), ADR-0005 (§2, §3), ADR-0010, ADR-0011

---

## Por que este cenário existe na PoC

Prova duas coisas distintas:

1. **Que dimensão que se liga por coluna — não por ID surrogate — se resolve sem
   join.** `order.business_unit_code` aponta para `business_unit.bu_code`. Não há
   chave técnica; a ligação é o próprio código de negócio. Dictionary transforma
   isso em `O(1)` em memória e **tira o join do plano de execução**.
2. **Que agregação sobre entidade mutável exige Refreshable MV.** Este é o
   cenário onde a tentação de usar `SummingMergeTree` incremental é mais forte
   (é literalmente "somar valor por dia") e onde ela produziria o número errado.

**Risco endereçado:** a armadilha 4 do `CLAUDE.md` §8 — dupla contagem. Ver
[§ Armadilhas](#armadilhas).

---

## Fontes

| Tabela | Objeto lido | Camada | Por que este e não outro |
|---|---|---|---|
| Pedido | `dh_core.v_sales__order_current` | L1 | Estado atual deduplicado. Ler a tabela core direto traria as N versões de cada pedido, e a soma sairia inflada (ADR-0004 §4). |
| Itens | `dh_core.v_sales__order_item_current` | L1 | Necessário para `items_count` e `units_sold`. Agregado por `order_id` **antes** do join com o pedido, para não multiplicar o valor do cabeçalho. |
| Unidade de negócio | `dh_core.dict__business_unit` | L2 | Centenas de linhas, lookup por chave exata. `LIFETIME(MIN 300 MAX 600)` mantém atualizado. |

**Intenção declarada do `dictGet`:** o nome e o canal da BU gravados são os
**vigentes no momento do refresh**, não no momento do pedido. Para relatório
gerencial isso é o desejado — renomear uma BU deve renomeá-la em todo o
histórico. Se algum dia o requisito virar "o nome que a BU tinha na data",
isso deixa de ser dictionary e passa a ser `ASOF JOIN` como no cenário 005.

---

## Modelo de saída

```sql
-- internal/contexts/analytics/sql/30-marts/0020__revenue_by_bu_day.sql

CREATE TABLE IF NOT EXISTS dh_marts.revenue_by_bu_day {ON_CLUSTER}
(
    order_date            Date,
    business_unit_code    LowCardinality(String),
    bu_name               String,
    bu_channel            LowCardinality(String),
    bu_region             LowCardinality(String),
    order_channel         LowCardinality(String),
    currency              LowCardinality(String),

    orders                UInt64,
    orders_canceled       UInt64,
    units_sold            Int64,
    items_count           UInt64,
    distinct_customers    UInt64,

    gross_revenue         Decimal(38,4),
    discount_total        Decimal(38,4),
    freight_total         Decimal(38,4),
    net_revenue           Decimal(38,4),
    avg_ticket            Decimal(38,4),

    _bu_found             UInt8,          -- 0 = business_unit_code sem cadastro
    _refreshed_at         DateTime64(3)
)
ENGINE = ReplacingMergeTree(_refreshed_at)
PARTITION BY toYYYYMM(order_date)
ORDER BY (business_unit_code, order_date, order_channel, currency);
```

Decisões do DDL:

- **`ORDER BY (business_unit_code, order_date, ...)`** — perfil analítico, e o
  filtro dominante é BU + intervalo de datas (ADR-0011 §1). A primeira coluna é
  `business_unit_code` porque quase todo relatório gerencial filtra por BU; quem
  quer "todas as BUs no mês" ainda se beneficia do *partition pruning* por
  `toYYYYMM(order_date)`.
- **`ReplacingMergeTree(_refreshed_at)`** e não `SummingMergeTree`. O mart é
  **substituído por janela**: o refresh reinsere as linhas dos últimos 3 dias, e o
  `Replacing` mantém a versão de `_refreshed_at` maior. Um `SummingMergeTree`
  aqui somaria o recálculo ao valor anterior — dobrando a receita a cada refresh.
- **`Decimal(38,4)`** nos agregados, não `Decimal(18,4)`: a soma de milhões de
  valores de 18 dígitos estoura a precisão. Nunca `Float64` (`dq.money.no_float`).
- **`_bu_found`** materializa o achado de qualidade: pedido com
  `business_unit_code` que não existe no cadastro. Não se descarta a linha — ela
  aparece com `_bu_found = 0` e alimenta `dq.rel.fk_violation`.

---

## Transformação

```sql
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_marts.mv__revenue_by_bu_day {ON_CLUSTER}
REFRESH EVERY 15 MINUTE APPEND
TO dh_marts.revenue_by_bu_day AS
WITH
    -- janela de recálculo: 3 dias (tolerância a late arrival, ADR-0004 §7)
    (today() - 3) AS window_from,

    -- itens agregados por pedido ANTES do join: evita multiplicar o cabeçalho
    order_items AS (
        SELECT
            order_id,
            sum(quantity)   AS units,
            count()         AS items
        FROM dh_core.v_sales__order_item_current
        WHERE toDate(created_at) >= window_from
        GROUP BY order_id
    )
SELECT
    toDate(o.created_at)                                        AS order_date,
    o.business_unit_code                                        AS business_unit_code,

    -- dimensão resolvida por dictionary: join por COLUNA, não por ID surrogate.
    -- COMPLEX_KEY_HASHED exige tuple() na chave, mesmo com chave de 1 coluna.
    dictGetOrDefault('dh_core.dict__business_unit', 'bu_name',
                     tuple(o.business_unit_code), '(sem cadastro)')   AS bu_name,
    dictGetOrDefault('dh_core.dict__business_unit', 'channel',
                     tuple(o.business_unit_code), 'unknown')          AS bu_channel,
    dictGetOrDefault('dh_core.dict__business_unit', 'region',
                     tuple(o.business_unit_code), 'unknown')          AS bu_region,

    o.channel                                                   AS order_channel,
    o.currency                                                  AS currency,

    countDistinct(o.order_id)                                   AS orders,
    countDistinctIf(o.order_id, o.order_status = 'canceled')    AS orders_canceled,
    sum(coalesce(oi.units, 0))                                  AS units_sold,
    sum(coalesce(oi.items, 0))                                  AS items_count,
    countDistinct(o.customer_id)                                AS distinct_customers,

    -- receita: pedido cancelado NÃO entra (regra 2)
    sumIf(o.gross_amount,    o.order_status != 'canceled')      AS gross_revenue,
    sumIf(o.discount_amount, o.order_status != 'canceled')      AS discount_total,
    sumIf(o.freight_amount,  o.order_status != 'canceled')      AS freight_total,
    sumIf(o.total_amount,    o.order_status != 'canceled')      AS net_revenue,

    if(countDistinctIf(o.order_id, o.order_status != 'canceled') = 0,
       toDecimal128(0, 4),
       sumIf(o.total_amount, o.order_status != 'canceled')
         / countDistinctIf(o.order_id, o.order_status != 'canceled')) AS avg_ticket,

    dictHas('dh_core.dict__business_unit', tuple(o.business_unit_code)) AS _bu_found,
    now64(3)                                                    AS _refreshed_at

FROM dh_core.v_sales__order_current AS o
LEFT JOIN order_items AS oi USING (order_id)
WHERE toDate(o.created_at) >= window_from
GROUP BY order_date, business_unit_code, bu_name, bu_channel, bu_region,
         order_channel, currency, _bu_found;
```

Nota sobre `APPEND`: o `Replacing` do destino é o que resolve a substituição — o
refresh reinsere as linhas da janela com `_refreshed_at` novo, e a versão antiga é
colapsada. Leitura correta do mart, portanto, também é por view:

```sql
CREATE VIEW IF NOT EXISTS dh_marts.v_revenue_by_bu_day {ON_CLUSTER} AS
SELECT * EXCEPT (_refreshed_at) FROM dh_marts.revenue_by_bu_day FINAL;
```

E o contrato de leitura em L3:

```sql
-- internal/contexts/analytics/sql/40-reports/0020__revenue_by_bu.sql
CREATE VIEW IF NOT EXISTS dh_reports.v_revenue_by_bu_last_30d {ON_CLUSTER} AS
SELECT business_unit_code, bu_name, bu_channel, order_date,
       orders, units_sold, gross_revenue, discount_total, net_revenue, avg_ticket
FROM dh_marts.v_revenue_by_bu_day
WHERE order_date >= today() - 30
ORDER BY business_unit_code, order_date;
```

---

## Regras de negócio

1. **Grão:** uma linha por (`order_date`, `business_unit_code`, `order_channel`,
   `currency`). `order_date` é `toDate(order.created_at)` — data de criação do
   pedido, não de faturamento.
2. **Pedido cancelado não gera receita**, mas é contado em `orders_canceled` e em
   `orders`. Isso permite calcular taxa de cancelamento sem um segundo mart.
3. **`net_revenue` = `total_amount`** do cabeçalho, que já é
   `gross - discount + freight` na origem. Não se recalcula a partir dos itens:
   se cabeçalho e itens divergirem, é um achado de qualidade
   (`dq.marts.header_item_parity`), não algo a corrigir silenciosamente.
4. **Moeda nunca é convertida nem somada entre moedas.** `currency` está no grão.
   Somar BRL com USD é proibido; um relatório consolidado exige uma tabela de
   câmbio, que está fora do escopo da PoC.
5. **`business_unit_code` sem cadastro** não é descartado: entra com
   `bu_name = '(sem cadastro)'` e `_bu_found = 0`.
6. **Pedido sem item** entra com `units_sold = 0` e `items_count = 0`. Pode ser
   *late arrival* (normal dentro da janela) ou órfão real (cenário 009).
7. **`avg_ticket`** exclui cancelados do numerador e do denominador. Divisão por
   zero retorna `0`, nunca nulo.
8. **`distinct_customers`** é `countDistinct` **por dia e BU** — não é somável
   entre dias. O consumidor que quiser "clientes únicos no mês" tem de recalcular
   do mart de cliente (cenário 008), não somar esta coluna.

---

## Armadilhas

### 1. `SummingMergeTree` incremental aqui produz receita inflada

É a armadilha 4 do `CLAUDE.md` §8, e este cenário é o exemplo canônico:

```
L0 recebe:  order_id=A  _cdc_seq=100  total=100.00   (created)
            order_id=A  _cdc_seq=140  total=150.00   (cliente adicionou item)

MV incremental -> SummingMergeTree:   100.00 + 150.00 = 250.00    ✗
Refreshable MV lendo v_*_current:     150.00                      ✓
```

E é pior com cancelamento: a v1 do pedido soma receita, a v2 `canceled` não
subtrai nada — o agregado incremental fica permanentemente inflado.

### 2. Ler `dh_core.sales__order` sem `FINAL`

Cada versão do pedido viraria uma linha do `GROUP BY`, inflando `orders` e
`gross_revenue`. Bloqueado por `no-direct-core-read.sh`.

### 3. Join com itens sem agregar antes

`order LEFT JOIN order_item USING (order_id)` multiplica o cabeçalho pelo número
de itens. Um pedido de 5 itens contribuiria com 5× o `total_amount`. Por isso a
CTE `order_items` agrega **antes** do join.

### 4. `countDistinct` dentro de agregado por janela

Se a janela reinsere 3 dias e o `ReplacingMergeTree` substitui as linhas,
`distinct_customers` está correto para o grão da linha. Mas **somar** essa coluna
entre linhas é errado. Documentado na regra 8, e é por isso que o mart não tem
uma linha de total.

### 5. `dictGet` com `COMPLEX_KEY_HASHED` e chave de uma coluna

Exige `tuple(col)`. Sem o `tuple`, o erro é de tipo em tempo de execução —
não silencioso, mas fácil de perder ao escrever o SQL.

### 6. Overflow de `Decimal(18,4)` na soma

`sum()` de `Decimal(18,4)` sobre milhões de linhas estoura. Destino é
`Decimal(38,4)`. `Float64` resolveria o overflow e introduziria erro de
arredondamento em dinheiro — proibido.

---

## Critérios de aceite

- [ ] **O mart bate com o cálculo independente** (o check mais importante):
  ```sql
  SELECT
      abs(m.net - d.net) AS diff
  FROM
      (SELECT sum(net_revenue) AS net FROM dh_marts.v_revenue_by_bu_day
       WHERE order_date >= today() - 30) m,
      (SELECT sum(total_amount) AS net FROM dh_core.v_sales__order_current
       WHERE toDate(created_at) >= today() - 30 AND order_status != 'canceled') d;
  -- esperado: diff = 0
  ```
- [ ] **Nenhuma dupla contagem após UPDATE de CDC**: produzir v1 (total=100) e v2
  (total=150) do mesmo pedido, aguardar refresh.
  ```sql
  SELECT net_revenue FROM dh_marts.v_revenue_by_bu_day
  WHERE business_unit_code = 'BU-TEST' AND order_date = today();
  -- esperado: 150.0000  (não 250.0000)
  ```
- [ ] **Cancelamento retira a receita**: produzir v3 com `order_status='canceled'`.
  ```sql
  -- esperado: net_revenue volta a 0 e orders_canceled = 1
  ```
- [ ] **Dimensão resolvida**: nenhuma linha com `_bu_found = 0` para BU cadastrada.
  ```sql
  SELECT count() FROM dh_marts.v_revenue_by_bu_day WHERE _bu_found = 0
    AND business_unit_code IN (SELECT bu_code FROM dh_core.v_organization__business_unit_current);
  -- esperado: 0
  ```
- [ ] **Dictionary carregado**:
  ```sql
  SELECT status, element_count, last_exception FROM system.dictionaries
  WHERE name = 'dict__business_unit';
  -- esperado: status = 'LOADED', last_exception = ''
  ```
- [ ] **Refresh saudável**:
  ```sql
  SELECT status, last_refresh_result, exception FROM system.view_refreshes
  WHERE view = 'mv__revenue_by_bu_day';
  -- esperado: last_refresh_result = 'Finished', exception = ''
  ```
- [ ] **Sem mistura de moedas**: a receita de cada moeda fecha isoladamente.
  ```sql
  SELECT m.currency, abs(m.net - d.net) AS diff
  FROM (SELECT currency, sum(net_revenue) AS net FROM dh_marts.v_revenue_by_bu_day
        WHERE order_date >= today() - 30 GROUP BY currency) m
  INNER JOIN (SELECT currency, sum(total_amount) AS net FROM dh_core.v_sales__order_current
        WHERE toDate(created_at) >= today() - 30 AND order_status != 'canceled'
        GROUP BY currency) d USING (currency);
  -- esperado: diff = 0 para toda moeda
  ```
- [ ] **Latência analítica**: `SELECT` de 90 dias para uma BU em < 1 s (p95),
  medido em `make bench`.

---

## Checks de qualidade associados

| check_id | Regra | Severidade |
|---|---|---|
| `dq.marts.sum_parity` | soma do mart == soma de `v_sales__order_current` (não cancelados) | error |
| `dq.marts.refresh_health` | `system.view_refreshes` sem exceção, `last_success` < 30 min | error |
| `dq.dict.loaded` | `dict__business_unit` com status `LOADED` | error |
| `dq.rel.fk_violation` | `_bu_found = 0` em menos de 0,1% das linhas | warn |
| `dq.money.no_float` | nenhuma coluna monetária `Float*` | error |
| `dq.marts.header_item_parity` | **novo** — divergência entre `total_amount` do cabeçalho e soma dos itens < 0,5% dos pedidos | warn |

---

## Consumo

Perfil analítico: consumido por BI e por analista via `dh_reports`, com o role
`dh_analyst` (ADR-0011 §4).

```sql
-- receita e taxa de cancelamento por BU no mês
SELECT bu_name,
       sum(orders)                                    AS orders,
       sum(orders_canceled) / sum(orders)             AS cancel_rate,
       sum(net_revenue)                               AS net_revenue,
       sum(net_revenue) / sum(orders - orders_canceled) AS avg_ticket
FROM dh_reports.v_revenue_by_bu_last_30d
GROUP BY bu_name
ORDER BY net_revenue DESC;
```

Não há endpoint aplicacional para este mart: `max_execution_time = 3` do role
`dh_app` não é compatível com varredura de 90 dias, e por desenho isso deve
falhar em vez de degradar o serviço.

---

## Arquivos no repositório

| Caminho | Conteúdo |
|---|---|
| `internal/contexts/analytics/sql/30-marts/0020__revenue_by_bu_day.sql` | tabela, Refreshable MV, view `v_revenue_by_bu_day` |
| `internal/contexts/analytics/sql/40-reports/0020__revenue_by_bu.sql` | `dh_reports.v_revenue_by_bu_last_30d` |
| `internal/contexts/organization/sql/30-marts/0010__dict_business_unit.sql` | `dh_core.dict__business_unit` |
| `internal/contexts/analytics/reports/revenue_by_bu.go` | query nomeada (uso de BI/CLI) |
| `internal/contexts/analytics/sql/40-reports/0021__dq_revenue_parity.sql` | `dq.marts.sum_parity`, `dq.marts.header_item_parity` |
| `test/e2e/revenue_by_bu_test.go` | cenários de UPDATE, cancelamento e late arrival |
