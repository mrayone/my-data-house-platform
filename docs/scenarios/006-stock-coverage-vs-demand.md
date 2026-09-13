# Cenário 006 — Cobertura de estoque vs demanda por centro de distribuição

- **Perfil:** analítico (consumo secundário aplicacional: alerta por CD)
- **Pergunta de negócio:** para cada par item × centro de distribuição, quantos dias
  de venda o estoque disponível ainda cobre, e quais pares já estão abaixo do
  estoque de segurança?
- **Mecanismo:** **Refreshable MV** com substituição total do destino (ADR-0005,
  árvore de decisão: correlação entre **duas entidades mutáveis** — `stock_position`
  e `order_item`/`order` — logo nem MV incremental nem dictionary servem).
- **Frescor esperado:** até 15 min (`REFRESH EVERY 15 MINUTE`).
- **Janela de recálculo:** o mart é um **snapshot do estado corrente**, recalculado
  integralmente a cada refresh. A janela existe na **demanda**: 28 dias corridos
  (`demand_basis_from` / `demand_basis_to` gravados na linha).
- **Entidades envolvidas:** `inventory.stock_position`, `sales.order_item`,
  `sales.order`
- **Tipo de correlação:** **chave composta** (`item_id` + `dc_id`) entre entidade de
  alta taxa de atualização (posição de estoque) e agregado de demanda; a chave do
  lado da demanda é **parcialmente nula** (`order_item.dc_id` é `Nullable`).
- **ADRs relevantes:** ADR-0001 (camadas), ADR-0004 (§4 leitura via view, §6
  agregação sobre mutável, §7 fora de ordem), ADR-0005 (§2 Refreshable MV),
  ADR-0010 (checks), ADR-0011 (`ORDER BY` por perfil de acesso)

---

## Por que este cenário existe na PoC

Três capacidades que nenhum cenário anterior cobre:

1. **Chave composta entre contextos diferentes.** Os cenários 001–005 correlacionam
   por `order_id` (uma coluna) ou por cadastro (`bu_code`, `discount_code`). Aqui a
   junção só fecha com **duas colunas simultâneas**, e uma delas é opcional no lado
   da demanda. É o caso que mais aparece no SAP real (material × centro).
2. **Entidade de alta taxa de atualização.** `stock_position` é reescrita a cada
   movimento; é a entidade que mais estressa o `ReplacingMergeTree` e a leitura via
   `v_*_current`. Se o custo de `FINAL`/`argMax` for proibitivo em alguma entidade,
   é nesta. O número medido aqui entra em `docs/evaluation/`.
3. **Demanda é agregação sobre dado mutável.** Um pedido cancelado **reduz** a
   demanda histórica. Isso derruba qualquer `SummingMergeTree` alimentado
   incrementalmente (ADR-0004 §6) e é a justificativa prática da Refreshable MV.

Além disso, o cenário produz o **contraexemplo** do 007: aqui só aparece par que
**tem** posição de estoque. O que foi vendido e não tem posição é, por construção,
invisível neste mart — e é exatamente o produto do cenário 007.

## Fontes

| Fonte | Camada | Papel | Observação |
|---|---|---|---|
| `dh_core.v_inventory__stock_position_current` | L1 | lado esquerdo, grão `(item_id, dc_id)` | define o universo de pares do mart |
| `dh_core.v_sales__order_item_current` | L1 | quantidade vendida por item e (talvez) CD | `dc_id` é `Nullable(String)` |
| `dh_core.v_sales__order_current` | L1 | filtro de status e data do pedido | `order_status` decide o que é demanda |

Leitura **sempre** pelas views `v_*_current` (ADR-0004 §4). Nenhum acesso a
`dh_landing.*` e nenhum acesso à tabela core direto.

### O problema do `dc_id` nulo em `order_item`

`dh_core.sales__order_item.dc_id` é `Nullable(String)` porque, no SAP replicado, o
centro de distribuição do item só é determinado quando a ordem de separação é
criada. Consequências:

- Item vendido **antes** da alocação tem `dc_id = NULL`. Essa demanda é real, mas
  **não é atribuível** a um CD específico.
- `NULL` não casa em `JOIN` (`NULL = 'DC01'` é `NULL`, não `false`). Se você
  simplesmente fizer `INNER JOIN ... USING (item_id, dc_id)`, essa demanda
  **desaparece** silenciosamente e a cobertura fica **superestimada** — o pior tipo
  de erro, porque o resultado parece saudável.
- `NULL` em coluna de `GROUP BY` é agrupado como um bucket próprio no ClickHouse
  (diferente de SQL padrão em alguns detalhes), o que gera uma linha fantasma
  `dc_id = NULL` se não for tratada.

O mart resolve com **três números explícitos**, nunca com uma escolha escondida:

| Coluna | Significado |
|---|---|
| `demand_qty_28d` | demanda **atribuída** ao par `(item_id, dc_id)` |
| `demand_qty_unassigned_28d` | demanda do `item_id` **sem CD**, replicada na linha de cada CD daquele item (é um total do item, não do par) |
| `demand_qty_allocated_28d` | demanda atribuída **+ rateio** da não atribuída |

e publica `unassigned_ratio` para o consumidor saber quanto do número é estimado.
O rateio é regra de negócio declarada (RN-5), não um detalhe de implementação.

## Modelo de saída

```sql
-- internal/contexts/analytics/sql/30-marts/0060__stock_coverage_dc_item.sql

CREATE TABLE IF NOT EXISTS dh_marts.stock_coverage_dc_item {ON_CLUSTER}
(
    -- chave composta do mart
    dc_id                        LowCardinality(String),
    item_id                      String,

    -- posição de estoque (estado corrente)
    dc_name                      String,
    on_hand                      Int64,
    reserved                     Int64,
    available                    Int64,
    safety_stock                 Int64,
    position_at                  DateTime64(3),
    position_age_seconds         UInt32,

    -- demanda observada na janela
    demand_qty_28d               Int64,
    demand_qty_7d                Int64,
    demand_qty_unassigned_28d    Int64,
    demand_qty_allocated_28d     Decimal(18,4),
    unassigned_ratio             Decimal(18,4),
    dc_count_for_item            UInt16,

    -- cobertura
    avg_daily_demand_28d         Decimal(18,4),
    avg_daily_demand_alloc_28d   Decimal(18,4),
    coverage_days                Nullable(Decimal(18,4)),
    coverage_days_allocated      Nullable(Decimal(18,4)),
    days_to_safety_stock         Nullable(Decimal(18,4)),

    -- classificação
    below_safety_stock           UInt8,
    coverage_class               LowCardinality(String),

    -- rastreabilidade do cálculo
    demand_basis_from            DateTime64(3),
    demand_basis_to              DateTime64(3),
    refreshed_at                 DateTime64(3)
)
ENGINE = MergeTree
ORDER BY (dc_id, item_id);
```

**Justificativa de `ORDER BY (dc_id, item_id)`** — ADR-0011 §1: a primeira coluna é
aquela pela qual o acesso dominante filtra. O consumo é "a lista de risco do CD X"
(planejador de abastecimento é por CD) e o alerta operacional é por CD. A consulta
por item em todos os CDs existe, mas é minoritária e resolve com filtro em
`item_id` sobre um conjunto já pequeno (um CD tem ordens de 10⁵ itens, não 10⁹).

**Sem `PARTITION BY`** — justificativa: o mart é um snapshot de cardinalidade
`itens × CDs` (ordem de 10⁵–10⁶ linhas), substituído **inteiro** a cada refresh.
Particionar aqui só fragmentaria as parts e aumentaria o custo do `EXCHANGE` do
refresh, sem nenhum ganho de poda — não há coluna de data pela qual se filtre.

```sql
-- Refreshable MV que mantém o mart (substituição total, sem APPEND)
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_marts.mv__stock_coverage_dc_item {ON_CLUSTER}
REFRESH EVERY 15 MINUTE
TO dh_marts.stock_coverage_dc_item
AS
<SELECT da seção Transformação>;
```

**Por que substituição total e não `APPEND`:** o mart é estado corrente. `APPEND`
exigiria `ReplacingMergeTree` + `FINAL` na leitura (custo em todo consumidor) e
deixaria par que **deixou de existir** (item descontinuado no CD) como linha
fantasma para sempre. A substituição total é atômica (`EXCHANGE TABLES`) e resolve
remoção de graça.

## Transformação

```sql
-- internal/contexts/analytics/sql/30-marts/0060__stock_coverage_dc_item.sql (2º statement)

CREATE MATERIALIZED VIEW IF NOT EXISTS dh_marts.mv__stock_coverage_dc_item {ON_CLUSTER}
REFRESH EVERY 15 MINUTE
TO dh_marts.stock_coverage_dc_item
AS
WITH
    /* -------- parâmetros do cálculo (versionados no SQL, não em config) -------- */
    now64(3)                                        AS ref_ts,
    toDateTime64(ref_ts - INTERVAL 28 DAY, 3)       AS demand_from,
    toDateTime64(ref_ts - INTERVAL 7 DAY,  3)       AS demand_from_7d,
    28                                              AS window_days,
    /* status que representam demanda real; 'created' ainda não é venda,
       'canceled' deixa de ser (ver RN-2) */
    ['paid','invoiced','shipped','delivered']       AS billable_status,

    /* -------- 1. posição de estoque corrente, grão (item_id, dc_id) -------- */
    stock AS
    (
        SELECT
            item_id,
            dc_id,
            dc_name,
            on_hand,
            reserved,
            available,
            safety_stock,
            position_at
        FROM dh_core.v_inventory__stock_position_current
    ),

    /* -------- 2. demanda ATRIBUÍDA: item vendido com CD conhecido -------- */
    demand_dc AS
    (
        SELECT
            i.item_id                                               AS item_id,
            assumeNotNull(i.dc_id)                                  AS dc_id,
            sum(i.quantity)                                         AS demand_qty_28d,
            sumIf(i.quantity, o.created_at >= demand_from_7d)       AS demand_qty_7d
        FROM dh_core.v_sales__order_item_current AS i
        INNER JOIN dh_core.v_sales__order_current AS o USING (order_id)
        WHERE o.created_at >= demand_from
          AND o.order_status IN billable_status
          AND i.dc_id IS NOT NULL
          AND i.dc_id != ''            -- string vazia é tão inútil quanto NULL
        GROUP BY item_id, dc_id
    ),

    /* -------- 3. demanda NÃO ATRIBUÍDA: item vendido sem CD -------- */
    demand_unassigned AS
    (
        SELECT
            i.item_id                   AS item_id,
            sum(i.quantity)             AS demand_qty_unassigned_28d
        FROM dh_core.v_sales__order_item_current AS i
        INNER JOIN dh_core.v_sales__order_current AS o USING (order_id)
        WHERE o.created_at >= demand_from
          AND o.order_status IN billable_status
          AND (i.dc_id IS NULL OR i.dc_id = '')
        GROUP BY item_id
    ),

    /* -------- 4. base de rateio: participação de cada CD no disponível do item -- */
    item_stock AS
    (
        SELECT
            item_id,
            sum(greatest(available, 0))     AS item_available,
            count()                         AS dc_count_for_item
        FROM stock
        GROUP BY item_id
    ),

    /* -------- 5. total atribuído por item (para o unassigned_ratio) -------- */
    item_demand AS
    (
        SELECT item_id, sum(demand_qty_28d) AS item_demand_attributed
        FROM demand_dc
        GROUP BY item_id
    )

SELECT
    s.dc_id                                                     AS dc_id,
    s.item_id                                                   AS item_id,
    s.dc_name                                                   AS dc_name,
    s.on_hand                                                   AS on_hand,
    s.reserved                                                  AS reserved,
    s.available                                                 AS available,
    s.safety_stock                                              AS safety_stock,
    s.position_at                                               AS position_at,
    toUInt32(greatest(dateDiff('second', s.position_at, ref_ts), 0))
                                                                AS position_age_seconds,

    d.demand_qty_28d                                            AS demand_qty_28d,
    d.demand_qty_7d                                             AS demand_qty_7d,
    u.demand_qty_unassigned_28d                                  AS demand_qty_unassigned_28d,
    toUInt16(ist.dc_count_for_item)                             AS dc_count_for_item,

    /* rateio da demanda sem CD: proporcional ao disponível do CD;
       se o item não tem disponível em nenhum CD, rateio igual entre os CDs (RN-5) */
    toDecimal64(d.demand_qty_28d, 4)
      + toDecimal64(u.demand_qty_unassigned_28d, 4)
        * if(ist.item_available > 0,
             divideDecimal(toDecimal64(greatest(s.available, 0), 4),
                           toDecimal64(ist.item_available, 4), 6),
             divideDecimal(toDecimal64(1, 4),
                           toDecimal64(greatest(ist.dc_count_for_item, 1), 4), 6))
                                                                AS demand_qty_allocated_28d,

    /* fração do número que é estimada, no nível do item */
    if(idm.item_demand_attributed + u.demand_qty_unassigned_28d > 0,
       divideDecimal(toDecimal64(u.demand_qty_unassigned_28d, 4),
                     toDecimal64(idm.item_demand_attributed
                                 + u.demand_qty_unassigned_28d, 4), 4),
       toDecimal64(0, 4))                                       AS unassigned_ratio,

    divideDecimal(toDecimal64(d.demand_qty_28d, 4),
                  toDecimal64(window_days, 4), 4)               AS avg_daily_demand_28d,
    divideDecimal(demand_qty_allocated_28d,
                  toDecimal64(window_days, 4), 4)               AS avg_daily_demand_alloc_28d,

    /* cobertura: NULL quando não há demanda — 'infinito' não é um número (RN-7) */
    if(avg_daily_demand_28d > 0,
       divideDecimal(toDecimal64(greatest(s.available, 0), 4), avg_daily_demand_28d, 2),
       NULL)                                                    AS coverage_days,
    if(avg_daily_demand_alloc_28d > 0,
       divideDecimal(toDecimal64(greatest(s.available, 0), 4),
                     avg_daily_demand_alloc_28d, 2),
       NULL)                                                    AS coverage_days_allocated,
    if(avg_daily_demand_alloc_28d > 0,
       divideDecimal(toDecimal64(s.available - s.safety_stock, 4),
                     avg_daily_demand_alloc_28d, 2),
       NULL)                                                    AS days_to_safety_stock,

    toUInt8(s.available < s.safety_stock)                       AS below_safety_stock,

    multiIf(
        s.available <= 0,                                       'stockout',
        avg_daily_demand_alloc_28d = 0,                         'no_demand',
        coverage_days_allocated <  7,                           'critical',
        coverage_days_allocated < 21,                           'low',
        coverage_days_allocated < 90,                           'healthy',
                                                                'excess'
    )                                                           AS coverage_class,

    demand_from                                                 AS demand_basis_from,
    ref_ts                                                      AS demand_basis_to,
    ref_ts                                                      AS refreshed_at

FROM stock AS s
LEFT JOIN demand_dc  AS d   ON s.item_id = d.item_id AND s.dc_id = d.dc_id
LEFT JOIN demand_unassigned AS u   ON s.item_id = u.item_id
LEFT JOIN item_stock AS ist ON s.item_id = ist.item_id
LEFT JOIN item_demand AS idm ON s.item_id = idm.item_id;
```

Notas de implementação que não são óbvias:

- `divideDecimal(a, b, scale)` é usado em toda divisão: a divisão `/` entre
  `Decimal` no ClickHouse resolve a escala do resultado por regra própria e pode
  truncar; `divideDecimal` fixa a escala explicitamente. **Nenhuma** conta passa
  por `Float` (ADR-0010, `dq.money.no_float`).
- `LEFT JOIN` com CTE ausente devolve o **zero do tipo** (`0` para `Int64`), não
  `NULL`, porque as colunas das CTEs não são `Nullable`. Isso é intencional:
  "nenhuma venda" é demanda zero, não demanda desconhecida.
- Os aliases `avg_daily_demand_28d`, `demand_qty_allocated_28d` e
  `coverage_days_allocated` são reaproveitados em expressões posteriores do mesmo
  `SELECT`; o ClickHouse permite (diferente de SQL padrão) e isso evita repetir a
  expressão quatro vezes. Se algum dia o `SELECT` for reescrito para um dialeto
  estrito, essas expressões viram CTEs.

## Regras de negócio

1. **Universo do mart = posição de estoque.** Só existe linha para par
   `(item_id, dc_id)` presente em `v_inventory__stock_position_current`. Item
   vendido sem posição **não** aparece aqui — é produto do cenário 007.
2. **Demanda conta apenas pedido faturável.** `order_status ∈ {paid, invoiced,
   shipped, delivered}`. `created` é intenção, não demanda atendível; `canceled`
   **sai** da demanda no refresh seguinte ao cancelamento.
3. **A data da demanda é `order.created_at`**, não `order_item.created_at` nem
   `updated_at`. O item pode ser reescrito pelo CDC muito depois; a data do pedido
   é o que define o período comercial.
4. **Janela de demanda = 28 dias corridos** encerrando no instante do refresh.
   Escolha: 28 = 4 semanas inteiras, o que neutraliza sazonalidade de dia da
   semana. `demand_basis_from`/`demand_basis_to` gravam a janela usada, para que o
   número seja reproduzível depois.
5. **Rateio da demanda sem CD:** proporcional ao `available` de cada CD do item; se
   o item tem `available` total ≤ 0 em todos os CDs, o rateio é **igual** entre os
   CDs onde o item tem posição. Justificativa: na ausência de informação de
   alocação, o disponível é o melhor proxy de para onde a separação iria.
6. **`coverage_days` oficial é `coverage_days_allocated`.** `coverage_days` (apenas
   demanda atribuída) é publicado para auditoria da diferença entre os dois. O
   relatório de negócio usa a versão com rateio; quem quiser o número "duro" tem a
   outra coluna e o `unassigned_ratio`.
7. **Demanda zero não produz cobertura infinita.** `coverage_days` é `NULL` e
   `coverage_class = 'no_demand'`. Publicar `999999` polui média, ordenação e
   gráfico.
8. **`available` negativo é tratado como zero** no numerador da cobertura
   (`greatest(available, 0)`), mas a coluna `available` preserva o valor original —
   negativo é sinal de inconsistência de estoque na origem e não deve ser escondido.
9. **`below_safety_stock` usa `available`, não `on_hand`.** Reservado já está
   comprometido com pedido existente.
10. **`days_to_safety_stock` pode ser negativo** — e isso é informação: mede o
    quanto já se está abaixo do piso, em dias de venda.

## Armadilhas

Referência: `CLAUDE.md` §8.

- **§8.1 — MV incremental não é join de streams.** A tentação aqui é uma MV
  incremental em `dh_landing.inventory__stock_position_raw` com `JOIN` na demanda.
  Isso gravaria a demanda **vigente no instante do movimento de estoque** e nunca
  corrigiria: um pedido cancelado depois continuaria contando. É proibido por
  ADR-0005 e barrado por `scripts/checks/no-join-in-incremental-mv.sh`.
- **§8.2 / §8.3 — leitura de `ReplacingMergeTree`.** `stock_position` é a entidade
  com maior taxa de reescrita do projeto. Ler
  `dh_core.inventory__stock_position` direto devolve **múltiplas posições para o
  mesmo par** e a cobertura sai multiplicada. Só
  `v_inventory__stock_position_current`.
- **§8.4 — agregação sobre mutável.** `SummingMergeTree` de demanda por
  `(item_id, dc_id)` alimentado de L0 soma todas as versões do mesmo
  `order_item`: um item reescrito 4 vezes pelo CDC vira 4× a quantidade. É a causa
  raiz de dupla contagem; o check `dq.marts.sum_parity` existe para pegá-la.
- **§8.6 — `ORDER BY` errado.** Se o `ORDER BY` fosse `(item_id, dc_id)`, o alerta
  por CD (acesso dominante) varreria o mart inteiro.
- **Específica deste cenário — `dc_id` nulo.** `INNER JOIN ... USING (item_id,
  dc_id)` descarta a demanda não alocada e **superestima** a cobertura. Ver seção
  "O problema do `dc_id` nulo".
- **Específica deste cenário — `position_at` velho.** Cobertura calculada sobre
  posição de estoque de 3 dias atrás é ficção. `position_age_seconds` está na linha
  para que o consumidor possa descartar; o check
  `dq.landing.freshness[inventory.stock_position]` é o guarda-chuva.
- **Rateio contaminando o agregado.** `demand_qty_unassigned_28d` é um total **do
  item**, repetido na linha de cada CD. Somar essa coluna sobre o mart
  **multiplica** a demanda pelo número de CDs. Para totalizar demanda sem CD:
  `SELECT sum(x) FROM (SELECT any(demand_qty_unassigned_28d) AS x FROM ... GROUP BY item_id)`.
  Está documentado na view de consumo (`dh_reports`) para que ninguém precise saber
  disso.

## Critérios de aceite

- [ ] **A1 — O mart existe e é preenchido.**
  ```sql
  SELECT count() FROM dh_marts.stock_coverage_dc_item;
  ```
  Esperado: igual ao número de pares em `v_inventory__stock_position_current`.
  ```sql
  SELECT
      (SELECT count() FROM dh_marts.stock_coverage_dc_item)                       AS mart_rows,
      (SELECT count() FROM dh_core.v_inventory__stock_position_current)           AS stock_rows,
      mart_rows = stock_rows                                                      AS ok;
  ```
  Esperado: `ok = 1`.

- [ ] **A2 — Grão único (chave composta não duplica).**
  ```sql
  SELECT count() FROM (
      SELECT dc_id, item_id, count() AS c
      FROM dh_marts.stock_coverage_dc_item
      GROUP BY dc_id, item_id HAVING c > 1
  );
  ```
  Esperado: `0`.

- [ ] **A3 — Paridade de demanda (`dq.marts.sum_parity`): o mart não conta duas vezes.**
  ```sql
  WITH
      toDateTime64(now64(3) - INTERVAL 28 DAY, 3) AS demand_from,
      ['paid','invoiced','shipped','delivered']   AS billable_status
  SELECT
      (SELECT sum(demand_qty_28d) FROM dh_marts.stock_coverage_dc_item)   AS mart_demand,
      (SELECT sum(i.quantity)
         FROM dh_core.v_sales__order_item_current AS i
         INNER JOIN dh_core.v_sales__order_current AS o USING (order_id)
        WHERE o.created_at >= demand_from
          AND o.order_status IN billable_status
          AND i.dc_id IS NOT NULL AND i.dc_id != ''
          AND (i.item_id, assumeNotNull(i.dc_id)) IN
              (SELECT item_id, dc_id FROM dh_core.v_inventory__stock_position_current)
      )                                                                  AS direct_demand,
      mart_demand = direct_demand                                        AS ok;
  ```
  Esperado: `ok = 1`. A restrição `IN (pares com posição)` é necessária porque o
  mart só cobre pares com estoque (RN-1).

- [ ] **A4 — Nenhuma linha fantasma de CD nulo.**
  ```sql
  SELECT count() FROM dh_marts.stock_coverage_dc_item WHERE dc_id = '' OR dc_id IS NULL;
  ```
  Esperado: `0`.

- [ ] **A5 — Cobertura consistente com demanda zero.**
  ```sql
  SELECT count() FROM dh_marts.stock_coverage_dc_item
  WHERE (avg_daily_demand_alloc_28d = 0 AND coverage_days_allocated IS NOT NULL)
     OR (avg_daily_demand_alloc_28d > 0 AND coverage_days_allocated IS NULL);
  ```
  Esperado: `0`.

- [ ] **A6 — Cancelamento reduz a demanda (teste de autocorreção, e2e).**
  Produzir cancelamento de um pedido com item conhecido, esperar um refresh:
  ```sql
  SELECT demand_qty_28d FROM dh_marts.stock_coverage_dc_item
  WHERE item_id = 'ITM-TEST-1' AND dc_id = 'DC01';
  ```
  Esperado: valor **menor** que antes do cancelamento, exatamente pela quantidade
  do item cancelado. Este é o critério que prova a escolha da Refreshable MV.

- [ ] **A7 — Abaixo do estoque de segurança bate com o cálculo direto.**
  ```sql
  SELECT
      (SELECT count() FROM dh_marts.stock_coverage_dc_item WHERE below_safety_stock = 1) AS mart_cnt,
      (SELECT count() FROM dh_core.v_inventory__stock_position_current
        WHERE available < safety_stock)                                                  AS direct_cnt,
      mart_cnt = direct_cnt                                                              AS ok;
  ```
  Esperado: `ok = 1`.

- [ ] **A8 — Refresh saudável.**
  ```sql
  SELECT view, status, last_refresh_result, last_success_time, exception
  FROM system.view_refreshes WHERE view = 'mv__stock_coverage_dc_item';
  ```
  Esperado: `last_refresh_result = 'Finished'`, `exception` vazio,
  `last_success_time > now() - INTERVAL 30 MINUTE`.

- [ ] **A9 — Nenhuma coluna monetária ou de razão em `Float`.**
  ```sql
  SELECT name, type FROM system.columns
  WHERE database = 'dh_marts' AND table = 'stock_coverage_dc_item' AND type LIKE '%Float%';
  ```
  Esperado: vazio.

- [ ] **A10 — Latência do refresh registrada em `docs/evaluation/`.**
  ```sql
  SELECT view, last_refresh_time, last_success_duration_ms
  FROM system.view_refreshes WHERE view = 'mv__stock_coverage_dc_item';
  ```
  Esperado: duração medida e anotada; é insumo de dimensionamento do ClickHouse
  Cloud (ADR-0008).

## Checks de qualidade associados

Registrados em `dh_meta.dq_checks`, seguindo o padrão de ID do ADR-0010 §3.

| `check_id` | Classe | Regra | Threshold inicial | Severidade |
|---|---|---|---|---|
| `dq.marts.stock_coverage.sum_parity` | corretude | A3 (`mart_demand = direct_demand`) | delta = 0 | error |
| `dq.marts.stock_coverage.grain_unique` | corretude | A2 | 0 duplicatas | error |
| `dq.marts.refresh_health` | liveness | A8 para esta MV | `last_success` < 30 min | error |
| `dq.rel.unassigned_dc_ratio` | corretude | `avg(unassigned_ratio)` sobre itens com demanda | warn ≥ 0,05 · error ≥ 0,30 | warn |
| `dq.landing.freshness[inventory.stock_position]` | liveness | idade da última mensagem do tópico | warn ≥ 300 s · error ≥ 1800 s | error |
| `dq.marts.stock_coverage.position_age_p99` | corretude | `quantile(0.99)(position_age_seconds)` | < 3600 s | warn |
| `dq.money.no_float` | corretude | A9 | 0 colunas `Float*` | error |

`dq.rel.unassigned_dc_ratio` é o check que revela se a origem está alocando CD nos
itens. Se ele subir, a cobertura por CD perde significado — e é melhor saber isso
por métrica do que por reclamação de planejador.

## Consumo

**L3 — view de recorte (sem lógica nova, ADR-0001 §6):**

```sql
-- internal/contexts/analytics/sql/40-reports/0060__v_stock_coverage_alerts.sql

CREATE VIEW IF NOT EXISTS dh_reports.v_stock_coverage_alerts AS
SELECT
    dc_id, dc_name, item_id, available, safety_stock,
    avg_daily_demand_alloc_28d, coverage_days_allocated, days_to_safety_stock,
    coverage_class, unassigned_ratio, position_age_seconds, refreshed_at
FROM dh_marts.stock_coverage_dc_item
WHERE coverage_class IN ('stockout','critical','low') OR below_safety_stock = 1;

-- parameterized view: risco de um CD específico
CREATE VIEW IF NOT EXISTS dh_reports.v_stock_coverage_by_dc AS
SELECT * FROM dh_marts.stock_coverage_dc_item
WHERE dc_id = {dc_id:String}
ORDER BY coverage_days_allocated ASC NULLS LAST;
```

**API (ADR-0011 §5):**

| Endpoint | Fonte | Frescor declarado | Role |
|---|---|---|---|
| `GET /reports/stock-coverage?dc_id=DC01` | `dh_reports.v_stock_coverage_by_dc` | ≤ 15 min | `dh_analyst` |
| `GET /reports/stock-coverage/alerts` | `dh_reports.v_stock_coverage_alerts` | ≤ 15 min | `dh_app` |

O endpoint de alertas é o uso aplicacional: é uma leitura por `dc_id` sobre o mart
já materializado, sem `FINAL` e sem join — cabe no `max_execution_time=3` do role
`dh_app`.

**Analítico:** ranking de capital em risco por CD, evolução de `coverage_class` ao
longo do tempo (requer snapshot histórico — fora do escopo desta PoC; registrado
como evolução em `docs/evaluation/`).

## Arquivos no repositório

| Caminho | Conteúdo |
|---|---|
| `internal/contexts/analytics/sql/30-marts/0060__stock_coverage_dc_item.sql` | tabela + Refreshable MV |
| `internal/contexts/analytics/sql/40-reports/0060__v_stock_coverage_alerts.sql` | views L3 |
| `internal/contexts/analytics/sql/40-reports/0060__dq_stock_coverage.sql` | queries dos checks da tabela acima |
| `internal/contexts/analytics/reports/stock_coverage.go` | query versionada + handler |
| `test/e2e/stock_coverage_test.go` | A6 (cancelamento reduz demanda) e A3 |
| `docs/scenarios/006-stock-coverage-vs-demand.md` | este documento |

Contexto `analytics` porque o mart cruza `inventory` e `sales` (ADR-0009); por
ADR-0007 o contexto `analytics` é migrado por último dentro de cada camada,
garantindo que `v_*_current` de ambos os contextos já existam.
