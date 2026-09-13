# Cenário 007 — Ruptura: itens vendidos sem posição de estoque

- **Perfil:** analítico + operacional (o mesmo mart alimenta relatório e fila de ação)
- **Pergunta de negócio:** o que estamos vendendo sem ter estoque registrado para
  atender — e, de cada caso, é falta de dado ou falta de produto?
- **Mecanismo:** **Refreshable MV** com substituição total, construída sobre
  **anti-join** (ADR-0005 §4: "anti-join para achar o que **não** se conecta"). Não
  é MV incremental porque a ausência de uma linha **não gera insert** — não existe
  evento "o estoque não chegou", logo não há gatilho possível.
- **Frescor esperado:** até 15 min (`REFRESH EVERY 15 MINUTE`).
- **Janela de recálculo:** substituição total do destino; o recorte temporal está no
  **lado da venda** (28 dias para evidência, 7 dias para severidade) e na **janela
  de carência** de 72 h que separa "dado atrasado" de "dado ausente".
- **Entidades envolvidas:** `sales.order_item`, `sales.order`,
  `inventory.stock_position` (e `dh_landing.inventory__stock_position_raw` apenas
  como escalar de frescor — ver "Fontes")
- **Tipo de correlação:** **ausência de relação** (anti-join), em duas
  granularidades: por `item_id` e por chave composta `(item_id, dc_id)`.
- **ADRs relevantes:** ADR-0001 (§2 L0 não é consultada em produção — exceção
  documentada abaixo), ADR-0004 (§7 fora de ordem: tolerar, medir, nunca
  descartar), ADR-0005 (§4 anti-join), ADR-0010 (§4 orfandade é relatório),
  ADR-0011

---

## Por que este cenário existe na PoC

O escopo da PoC diz, textualmente, que existem tópicos que se conectam por ID,
outros por coluna qualquer, **e outros que não se conectam**. Os cenários 001–006
provam que sabemos juntar. Este prova que sabemos **transformar a ausência de junção
em produto**, que é a parte que quase nenhuma PoC de data platform entrega.

Três razões concretas:

1. **Anti-join é o teste mais duro do modelo de CDC.** Num pipeline com chegada fora
   de ordem (ADR-0004 §7), "não existe" e "ainda não chegou" são **a mesma consulta
   com respostas opostas**. Se o relatório não distinguir os dois, ele produz alarme
   falso todo dia e vira ruído — e um relatório que ninguém lê não sustenta decisão
   de contratação.
2. **É o complemento estrutural do 006.** O mart 006 tem como universo a posição de
   estoque; por construção, ele é **cego** para item vendido sem posição. Esse ponto
   cego é o produto deste cenário. Juntos, os dois cobrem o universo inteiro.
3. **É o cenário que mais depende de latência de ingestão**, então é o que melhor
   mede se a arquitetura L0→L1→L2 é rápida o suficiente para uso operacional.
   Número medido vai para `docs/evaluation/`.

## Fontes

| Fonte | Camada | Papel |
|---|---|---|
| `dh_core.v_sales__order_item_current` | L1 | lado presente do anti-join (o que foi vendido) |
| `dh_core.v_sales__order_current` | L1 | filtro de status/data e `order_id` distinto |
| `dh_core.v_inventory__stock_position_current` | L1 | lado ausente do anti-join |
| `dh_landing.inventory__stock_position_raw` | L0 | **apenas** `max(_kafka_ts)` — escalar de frescor do tópico |

### Exceção documentada ao ADR-0001 §2

ADR-0001 §2 diz que nada consulta L0 diretamente em produção. Este mart abre **uma**
exceção, restrita e justificada:

- O acesso é **um escalar agregado** (`max(_kafka_ts)`), nunca leitura linha-a-linha,
  nunca `JOIN` com L0.
- O dado só existe em L0: `_kafka_ts` é coluna técnica de landing (CLAUDE.md §7) e
  não sobrevive para L1. Sem ele, não há como distinguir "o registro de estoque
  deste item não veio" de "**nenhum** registro de estoque veio".
- O mart roda sob o role `dh_pipeline` (o refresh), não sob `dh_app`; o grant de
  `dh_app` em `dh_landing` continua inexistente (ADR-0011 §3).

O mesmo padrão e a mesma justificativa valem para o cenário 009 (freshness e
reconciliação). Se a PoC preferir fechar a exceção, a alternativa é materializar
`dh_meta.pipeline_health` (ADR-0010 §1) na Fase 08 e ler dela — o `SELECT` muda em
uma linha. Registrado como ponto de decisão em `docs/evaluation/`.

## Modelo de saída

```sql
-- internal/contexts/analytics/sql/30-marts/0070__stockout_candidates.sql

CREATE TABLE IF NOT EXISTS dh_marts.stockout_candidates {ON_CLUSTER}
(
    -- classe do achado: missing_stock_record | zero_available | missing_dc_position
    stockout_class         LowCardinality(String),

    -- chave; dc_id = '' quando a classe não tem CD determinado
    item_id                String,
    dc_id                  LowCardinality(String),
    dc_known               UInt8,

    -- evidência de venda
    first_sale_at          DateTime64(3),
    last_sale_at           DateTime64(3),
    evidence_age_seconds   UInt32,   -- idade de first_sale_at (a evidência mais ANTIGA)
    last_sale_age_seconds  UInt32,   -- idade de last_sale_at  (a evidência mais RECENTE)
    orders_affected        UInt32,
    units_sold_7d          Int64,
    units_sold_28d         Int64,
    revenue_at_risk        Decimal(18,4),

    -- lado do estoque (Nullable porque na classe (a) simplesmente não existe)
    available              Nullable(Int64),
    safety_stock           Nullable(Int64),
    position_at            Nullable(DateTime64(3)),

    -- decisão temporal
    inventory_lag_seconds  UInt32,
    grace_window_seconds   UInt32,
    evidence_status        LowCardinality(String), -- pending_grace | confirmed | suppressed_topic_lag
    severity               LowCardinality(String), -- low | medium | high

    refreshed_at           DateTime64(3)
)
ENGINE = MergeTree
ORDER BY (stockout_class, evidence_status, dc_id, item_id);
```

**Justificativa de `ORDER BY (stockout_class, evidence_status, dc_id, item_id)`** —
ADR-0011 §1. Todo consumo começa por "quais achados desta classe estão
confirmados": a fila operacional filtra
`stockout_class = 'zero_available' AND evidence_status = 'confirmed'`, e o relatório
analítico agrupa por classe. `dc_id` antes de `item_id` porque a ação é do CD.

**`dc_id` é `String` com sentinela `''`, não `Nullable(String)`** — justificativa:
`Nullable` em `ORDER BY` exige `allow_nullable_key=1`, piora a granularidade do
índice e transforma toda comparação em ternária. A coluna `dc_known` carrega a
informação que o `NULL` carregaria, sem custo no índice. Isso é decisão do mart, não
do core: em `dh_core.sales__order_item` o `dc_id` **continua** `Nullable`, fiel à
origem.

**Sem `PARTICIONAMENTO`** — a tabela é um snapshot de achados (esperado: 10²–10⁴
linhas em operação saudável, e se explodir para 10⁶ isso **é** o incidente).
Substituição total atômica a cada refresh.

```sql
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_marts.mv__stockout_candidates {ON_CLUSTER}
REFRESH EVERY 15 MINUTE
TO dh_marts.stockout_candidates
AS
<SELECT da seção Transformação>;
```

## Transformação

```sql
-- internal/contexts/analytics/sql/30-marts/0070__stockout_candidates.sql (2º statement)

CREATE MATERIALIZED VIEW IF NOT EXISTS dh_marts.mv__stockout_candidates {ON_CLUSTER}
REFRESH EVERY 15 MINUTE
TO dh_marts.stockout_candidates
AS
WITH
    /* ------------------------- parâmetros ------------------------- */
    now64(3)                                    AS ref_ts,
    toDateTime64(ref_ts - INTERVAL 28 DAY, 3)   AS sale_from,
    toDateTime64(ref_ts - INTERVAL 7 DAY,  3)   AS sale_from_7d,
    /* janela de carência: ADR-0004 §7 (late arrival padrão 72 h) */
    toUInt32(72 * 3600)                         AS grace_seconds,
    /* acima disso o TÓPICO está parado e nenhuma ausência é conclusiva */
    toUInt32(3600)                              AS topic_lag_tolerance_seconds,
    ['paid','invoiced','shipped','delivered']   AS billable_status,

    /* ---- frescor do tópico de estoque: único acesso a L0 (ver "Fontes") ---- */
    (SELECT toUInt32(greatest(dateDiff('second', max(_kafka_ts), ref_ts), 0))
       FROM dh_landing.inventory__stock_position_raw)
                                                AS inventory_lag_seconds,

    /* ---------------- vendas na janela, grão (item_id) ---------------- */
    sold_item AS
    (
        SELECT
            i.item_id                                                AS item_id,
            min(o.created_at)                                        AS first_sale_at,
            max(o.created_at)                                        AS last_sale_at,
            uniqExact(i.order_id)                                    AS orders_affected,
            sumIf(i.quantity, o.created_at >= sale_from_7d)          AS units_sold_7d,
            sum(i.quantity)                                          AS units_sold_28d,
            sum(i.total_amount)                                      AS revenue_28d
        FROM dh_core.v_sales__order_item_current AS i
        INNER JOIN dh_core.v_sales__order_current AS o USING (order_id)
        WHERE o.created_at >= sale_from
          AND o.order_status IN billable_status
        GROUP BY item_id
    ),

    /* ------------- vendas na janela, grão (item_id, dc_id) ------------- */
    sold_item_dc AS
    (
        SELECT
            i.item_id                                                AS item_id,
            assumeNotNull(i.dc_id)                                   AS dc_id,
            min(o.created_at)                                        AS first_sale_at,
            max(o.created_at)                                        AS last_sale_at,
            uniqExact(i.order_id)                                    AS orders_affected,
            sumIf(i.quantity, o.created_at >= sale_from_7d)          AS units_sold_7d,
            sum(i.quantity)                                          AS units_sold_28d,
            sum(i.total_amount)                                      AS revenue_28d
        FROM dh_core.v_sales__order_item_current AS i
        INNER JOIN dh_core.v_sales__order_current AS o USING (order_id)
        WHERE o.created_at >= sale_from
          AND o.order_status IN billable_status
          AND i.dc_id IS NOT NULL
          AND i.dc_id != ''
        GROUP BY item_id, dc_id
    ),

    /* -------------------- lado do estoque -------------------- */
    stock_dc AS
    (
        SELECT item_id, dc_id, available, safety_stock, position_at
        FROM dh_core.v_inventory__stock_position_current
    ),
    stock_item AS
    (
        SELECT item_id FROM stock_dc GROUP BY item_id
    )

/* ==================================================================== */
/* (a) item vendido que NÃO EXISTE em stock_position (nenhum CD)          */
/*     anti-join por item_id — cadastro faltando OU tópico atrasado       */
/* ==================================================================== */
SELECT
    'missing_stock_record'                                        AS stockout_class,
    s.item_id                                                     AS item_id,
    ''                                                            AS dc_id,
    toUInt8(0)                                                    AS dc_known,
    s.first_sale_at                                               AS first_sale_at,
    s.last_sale_at                                                AS last_sale_at,
    toUInt32(greatest(dateDiff('second', s.first_sale_at, ref_ts), 0)) AS evidence_age_seconds,
    toUInt32(greatest(dateDiff('second', s.last_sale_at,  ref_ts), 0)) AS last_sale_age_seconds,
    toUInt32(s.orders_affected)                                   AS orders_affected,
    s.units_sold_7d                                               AS units_sold_7d,
    s.units_sold_28d                                              AS units_sold_28d,
    s.revenue_28d                                                 AS revenue_at_risk,
    CAST(NULL AS Nullable(Int64))                                 AS available,
    CAST(NULL AS Nullable(Int64))                                 AS safety_stock,
    CAST(NULL AS Nullable(DateTime64(3)))                         AS position_at,
    inventory_lag_seconds                                         AS inventory_lag_seconds,
    grace_seconds                                                 AS grace_window_seconds,
    multiIf(
        inventory_lag_seconds > topic_lag_tolerance_seconds, 'suppressed_topic_lag',
        evidence_age_seconds  < grace_seconds,               'pending_grace',
                                                            'confirmed'
    )                                                             AS evidence_status,
    multiIf(evidence_status != 'confirmed',        'low',
            s.units_sold_7d > 0,                   'high',
                                                   'medium')      AS severity,
    ref_ts                                                        AS refreshed_at
FROM sold_item AS s
LEFT ANTI JOIN stock_item AS k USING (item_id)

UNION ALL

/* ==================================================================== */
/* (b) item COM posição, available <= 0 e venda recente                  */
/*     NÃO é anti-join: é fato positivo ("o estoque diz zero")           */
/* ==================================================================== */
SELECT
    'zero_available'                                              AS stockout_class,
    st.item_id                                                    AS item_id,
    st.dc_id                                                      AS dc_id,
    toUInt8(1)                                                    AS dc_known,
    s.first_sale_at                                               AS first_sale_at,
    s.last_sale_at                                                AS last_sale_at,
    toUInt32(greatest(dateDiff('second', s.first_sale_at, ref_ts), 0)) AS evidence_age_seconds,
    toUInt32(greatest(dateDiff('second', s.last_sale_at,  ref_ts), 0)) AS last_sale_age_seconds,
    toUInt32(s.orders_affected)                                   AS orders_affected,
    s.units_sold_7d                                               AS units_sold_7d,
    s.units_sold_28d                                              AS units_sold_28d,
    /* prospectivo: 7 dias da venda média recente ao preço médio praticado (RN-6) */
    multiIf(
        s.units_sold_7d <= 0, toDecimal64(0, 4),
        divideDecimal(s.revenue_28d, toDecimal64(greatest(s.units_sold_28d, 1), 4), 4)
            * toDecimal64(s.units_sold_7d, 4)
    )                                                             AS revenue_at_risk,
    st.available                                                  AS available,
    st.safety_stock                                               AS safety_stock,
    st.position_at                                                AS position_at,
    inventory_lag_seconds                                         AS inventory_lag_seconds,
    toUInt32(0)                                                   AS grace_window_seconds,
    /* presença de dado dispensa carência (RN-4) */
    'confirmed'                                                   AS evidence_status,
    multiIf(s.units_sold_7d > 0 AND st.available < 0,  'high',
            s.units_sold_7d > 0,                       'high',
                                                       'medium')  AS severity,
    ref_ts                                                        AS refreshed_at
FROM stock_dc AS st
INNER JOIN sold_item AS s USING (item_id)
WHERE st.available <= 0
  AND s.last_sale_at >= sale_from_7d

UNION ALL

/* ==================================================================== */
/* (c) item vendido em um dc_id que NÃO TEM posição para aquele item     */
/*     anti-join por CHAVE COMPOSTA, com o item presente em outro CD     */
/* ==================================================================== */
SELECT
    'missing_dc_position'                                         AS stockout_class,
    sd.item_id                                                    AS item_id,
    sd.dc_id                                                      AS dc_id,
    toUInt8(1)                                                    AS dc_known,
    sd.first_sale_at                                              AS first_sale_at,
    sd.last_sale_at                                               AS last_sale_at,
    toUInt32(greatest(dateDiff('second', sd.first_sale_at, ref_ts), 0)) AS evidence_age_seconds,
    toUInt32(greatest(dateDiff('second', sd.last_sale_at,  ref_ts), 0)) AS last_sale_age_seconds,
    toUInt32(sd.orders_affected)                                  AS orders_affected,
    sd.units_sold_7d                                              AS units_sold_7d,
    sd.units_sold_28d                                             AS units_sold_28d,
    sd.revenue_28d                                                AS revenue_at_risk,
    CAST(NULL AS Nullable(Int64))                                 AS available,
    CAST(NULL AS Nullable(Int64))                                 AS safety_stock,
    CAST(NULL AS Nullable(DateTime64(3)))                         AS position_at,
    inventory_lag_seconds                                         AS inventory_lag_seconds,
    grace_seconds                                                 AS grace_window_seconds,
    multiIf(
        inventory_lag_seconds > topic_lag_tolerance_seconds, 'suppressed_topic_lag',
        evidence_age_seconds  < grace_seconds,               'pending_grace',
                                                            'confirmed'
    )                                                             AS evidence_status,
    multiIf(evidence_status != 'confirmed', 'low',
            sd.units_sold_7d > 0,           'medium',
                                            'low')                AS severity,
    ref_ts                                                        AS refreshed_at
FROM sold_item_dc AS sd
/* o par (item, dc) não tem posição ... */
LEFT ANTI JOIN stock_dc AS sk ON sd.item_id = sk.item_id AND sd.dc_id = sk.dc_id
/* ... mas o item tem posição em ALGUM CD — senão o caso já é a classe (a) */
INNER JOIN stock_item AS ki ON sd.item_id = ki.item_id;
```

Notas de implementação:

- **`LEFT ANTI JOIN` é a forma canônica** do anti-join no ClickHouse e é o que o
  ADR-0005 §4 autoriza. Ele é preferível a `NOT IN (subquery)` porque o `NOT IN`
  com subquery que devolve `NULL` tem semântica ternária (qualquer `NULL` no lado
  direito faz o predicado nunca ser verdadeiro) — armadilha clássica que aqui
  esvaziaria o relatório em silêncio. `LEFT ANTI JOIN` não sofre disso.
- O `INNER JOIN stock_item` na classe (c) é o que garante **mutual exclusividade**
  entre (a) e (c): sem ele, todo item da classe (a) apareceria também em (c), uma vez
  por CD onde foi vendido.
- `revenue_at_risk` tem semântica diferente por classe (RN-6) e a coluna
  `stockout_class` é obrigatória em qualquer agregação dessa métrica.
- `uniqExact` e não `uniq`: o número vai para fila operacional e precisa ser exato;
  a cardinalidade é pequena o suficiente.

## Por que o anti-join precisa de janela temporal

Este é o ponto conceitual do cenário, e decorre direto do **ADR-0004 §7**:

> Item de pedido que chega antes do pedido **não é erro** — é normal. Nenhum passo do
> pipeline descarta linha órfã. Marts que correlacionam usam Refreshable MV, que
> reavalia periodicamente e **se autocorrige** quando o outro lado chega.

Aplicado a estoque: `sap.sales.order_item.v1` e `sap.inventory.stock_position.v1`
são tópicos independentes, com partições, connectors e ritmos independentes. Um item
novo é vendido no segundo em que entra no catálogo; a primeira posição de estoque
dele pode aterrissar minutos ou horas depois. **No instante do refresh, "não existe
posição" e "a posição ainda não chegou" são a mesma linha de resultado.**

Se o relatório tratar os dois como incidente:

- ele acusa dezenas ou centenas de falsos positivos por dia (todo item novo);
- o time para de olhar;
- quando houver um caso real, ninguém vê.

Se tratar os dois como normal, ele nunca acusa nada e não serve para nada.

A solução é **não decidir pela presença, e sim pela idade da evidência**:

| Grandeza | Coluna | Papel na decisão |
|---|---|---|
| Idade da evidência mais antiga | `evidence_age_seconds` (de `first_sale_at`) | é **ela** que compara com a carência — a evidência começou a existir na primeira venda |
| Idade da evidência mais recente | `last_sale_age_seconds` (de `last_sale_at`) | severidade: ausência com venda ontem é mais urgente que com venda de 3 semanas |
| Carência | `grace_window_seconds` = 72 h | ADR-0004 §7: mesma janela de *late arrival* declarada para os marts |
| Atraso do tópico | `inventory_lag_seconds` | **veto**: se o tópico está parado, nenhuma ausência é conclusiva |

Regras resultantes:

- `evidence_age_seconds < 72 h` → **`pending_grace`**: o caso é registrado e
  visível, mas não conta como incidente e não abre ação. É o "funcionamento
  esperado" do ADR-0004 §7, materializado.
- `evidence_age_seconds ≥ 72 h` → **`confirmed`**: o outro lado teve 72 h para
  chegar e não chegou. Agora é cadastro faltando (ou o connector do tópico de
  estoque está com filtro errado) — incidente, com runbook.
- `inventory_lag_seconds > 1 h` → **`suppressed_topic_lag`**: a tabela toda do
  estoque está velha. Neste estado, o anti-join é aritmeticamente correto e
  semanticamente inútil. Suprimir é melhor que emitir milhares de falsos
  confirmados, e a supressão é **visível na própria coluna** — não é um filtro
  escondido.
- A carência **não se aplica** à classe (b): ali existe um registro de estoque
  afirmando `available <= 0`. Ausência exige carência; **presença não**.

A escolha de 72 h não é arbitrária: é a mesma janela de recálculo que ADR-0005 §2
declara como padrão para *late arrival*. Se o p99 do atraso real observado
(`dq.rel.orphan_age_p99`, ADR-0010) subir acima disso, a janela do mart **e** esta
carência sobem juntas — são o mesmo número e precisam permanecer o mesmo número.

## Regras de negócio

1. **Venda considerada:** `order_status ∈ {paid, invoiced, shipped, delivered}`,
   `order.created_at` nos últimos 28 dias. Pedido `created` não indica compromisso
   de entrega; `canceled` sai do relatório no refresh seguinte.
2. **As três classes são mutuamente exclusivas.** Um `(item_id, dc_id)` aparece em,
   no máximo, uma classe:
   - (a) `missing_stock_record` — item sem **nenhuma** posição, em nenhum CD;
   - (b) `zero_available` — posição existe e afirma `available <= 0`;
   - (c) `missing_dc_position` — item tem posição em outro CD, mas não neste.
3. **Classe (a) é por item, classes (b) e (c) são por par.** Em (a), `dc_id = ''` e
   `dc_known = 0`, porque a pergunta "em qual CD falta?" não tem resposta quando não
   há nenhum CD.
4. **Carência só vale para ausência.** (a) e (c) são anti-joins → `pending_grace`
   antes de 72 h. (b) é presença de dado → `confirmed` imediatamente.
5. **Atraso do tópico de estoque acima de 1 h veta a conclusão** de (a) e (c),
   marcando `suppressed_topic_lag`. (b) não é vetada: se a posição é velha mas diz
   zero, e houve venda nos últimos 7 dias, o risco é real.
6. **`revenue_at_risk` tem duas semânticas, declaradas por classe:**
   - (a) e (c): **retrospectivo** — `sum(order_item.total_amount)` já vendido sem
     cobertura de estoque na janela de 28 dias;
   - (b): **prospectivo** — 7 dias de venda na cadência recente, ao preço médio
     praticado na janela (`revenue_28d / units_sold_28d × units_sold_7d`).
   Nunca somar a coluna sem agrupar por `stockout_class`.
7. **Preço médio praticado vem de `order_item`**, não de `pricing__prices`. O
   relatório mede risco sobre o que foi efetivamente cobrado; adesão a preço de
   tabela é assunto do cenário 005.
8. **`available` negativo é mantido** e eleva a severidade: indica venda além do
   estoque, não só ausência dele.
9. **Severidade:** `high` quando há venda nos últimos 7 dias com evidência
   `confirmed` (classes a, b); `medium` para (c) com venda recente ou (a)/(b) sem
   venda em 7 dias; `low` para tudo que não está `confirmed`. Ordem de ação
   operacional = `severity` desc, `revenue_at_risk` desc.
10. **Nenhuma linha é descartada por ser órfã** (ADR-0004 §7). A classificação
    temporal é o mecanismo; filtrar na origem seria apagar o produto.

## Armadilhas

Referência: `CLAUDE.md` §8.

- **§8.1 — MV incremental é trigger de INSERT.** Não existe insert quando um dado
  **falta**. Qualquer tentativa de manter este mart por MV incremental é
  conceitualmente impossível, não só arriscada. Só Refreshable MV (ou query em L3).
- **§8.2 / §8.3 — leitura de `ReplacingMergeTree`.** Aqui o erro é catastrófico e
  invertido: ler `dh_core.inventory__stock_position` direto pode mostrar uma versão
  **antiga** com `available > 0` e **esconder** a ruptura; ou mostrar a versão
  deletada (`_is_deleted = 1`) como se a posição existisse, **esvaziando** a classe
  (a). Anti-join sobre tabela não deduplicada é a pior combinação possível do
  ClickHouse. Só `v_inventory__stock_position_current`.
- **§8.5 — delete é coluna.** Item descontinuado no CD tem `_is_deleted = 1`, não
  desaparece da tabela. A view corrente é o que faz o anti-join enxergar a exclusão;
  sem ela, o relatório nunca detectaria descontinuação.
- **`NOT IN` com `NULL`.** `WHERE (item_id, dc_id) NOT IN (SELECT item_id, dc_id
  FROM ...)` onde o lado direito pode conter `NULL` devolve conjunto vazio sem
  erro nenhum. Use `LEFT ANTI JOIN`.
- **`dc_id` nulo virando classe (c) falsa.** Item vendido com `dc_id = NULL` não
  pertence a nenhum CD e é filtrado de `sold_item_dc`. Se entrasse, `assumeNotNull`
  produziria `''` e criaria uma classe (c) para um "CD vazio" que não existe.
- **Sobreposição de classes inflando o relatório.** Sem o `INNER JOIN stock_item` na
  classe (c), cada item da classe (a) reapareceria uma vez por CD vendido — o mesmo
  problema contado N vezes, com `revenue_at_risk` somando N vezes.
- **Somar `revenue_at_risk` entre classes.** Mistura retrospectivo com prospectivo.
  A view de consumo em `dh_reports` já agrega por classe para evitar o erro.
- **Refresh vazio apagando o mart.** Substituição total com `SELECT` que falha
  **mantém** o dado anterior (o `EXCHANGE` não acontece) — comportamento desejado.
  Mas um `SELECT` que legitimamente devolve zero linhas **zera** a tabela; é
  correto, e por isso a ausência de linhas precisa ser distinguível de falha de
  refresh via `system.view_refreshes` (check `dq.marts.refresh_health`).

## Critérios de aceite

- [ ] **A1 — As três classes existem e são mutuamente exclusivas.**
  ```sql
  SELECT stockout_class, count() FROM dh_marts.stockout_candidates GROUP BY stockout_class ORDER BY 1;
  -- esperado: as 3 classes presentes no dataset de seed

  SELECT count() FROM (
      SELECT item_id, dc_id, uniqExact(stockout_class) AS c
      FROM dh_marts.stockout_candidates
      GROUP BY item_id, dc_id HAVING c > 1
  );
  -- esperado: 0
  ```

- [ ] **A2 — Classe (a) bate com o anti-join direto.**
  ```sql
  WITH toDateTime64(now64(3) - INTERVAL 28 DAY, 3) AS sale_from,
       ['paid','invoiced','shipped','delivered']   AS billable_status
  SELECT
    (SELECT count() FROM dh_marts.stockout_candidates
      WHERE stockout_class = 'missing_stock_record')                             AS mart_cnt,
    (SELECT uniqExact(i.item_id)
       FROM dh_core.v_sales__order_item_current AS i
       INNER JOIN dh_core.v_sales__order_current AS o USING (order_id)
      WHERE o.created_at >= sale_from AND o.order_status IN billable_status
        AND i.item_id NOT IN (SELECT item_id FROM dh_core.v_inventory__stock_position_current))
                                                                                AS direct_cnt,
    mart_cnt = direct_cnt                                                       AS ok;
  ```
  Esperado: `ok = 1`. (Aqui o `NOT IN` é seguro porque `stock_position.item_id` é
  `String` não-nulo; no mart usamos `LEFT ANTI JOIN` por princípio.)

- [ ] **A3 — Classe (b) bate com o cálculo direto.**
  ```sql
  SELECT
    (SELECT count() FROM dh_marts.stockout_candidates WHERE stockout_class = 'zero_available') AS mart_cnt,
    (SELECT count() FROM dh_core.v_inventory__stock_position_current AS st
      WHERE st.available <= 0
        AND st.item_id IN (
            SELECT i.item_id FROM dh_core.v_sales__order_item_current AS i
            INNER JOIN dh_core.v_sales__order_current AS o USING (order_id)
            WHERE o.created_at >= now64(3) - INTERVAL 7 DAY
              AND o.order_status IN ('paid','invoiced','shipped','delivered')))               AS direct_cnt,
    mart_cnt = direct_cnt                                                                      AS ok;
  ```
  Esperado: `ok = 1`.

- [ ] **A4 — Classe (c) não contém item sem nenhuma posição.**
  ```sql
  SELECT count() FROM dh_marts.stockout_candidates AS s
  WHERE s.stockout_class = 'missing_dc_position'
    AND s.item_id NOT IN (SELECT item_id FROM dh_core.v_inventory__stock_position_current);
  ```
  Esperado: `0`.

- [ ] **A5 — Carência aplicada corretamente.**
  ```sql
  SELECT count() FROM dh_marts.stockout_candidates
  WHERE stockout_class IN ('missing_stock_record','missing_dc_position')
    AND evidence_status = 'confirmed'
    AND evidence_age_seconds < grace_window_seconds;
  -- esperado: 0

  SELECT count() FROM dh_marts.stockout_candidates
  WHERE stockout_class = 'zero_available' AND evidence_status != 'confirmed';
  -- esperado: 0 (presença de dado não tem carência — RN-4)
  ```

- [ ] **A6 — Autocorreção: posição que chega tarde remove o candidato (e2e).**
  Produzir venda de `ITM-LATE-1` sem posição de estoque; aguardar 1 refresh:
  ```sql
  SELECT stockout_class, evidence_status FROM dh_marts.stockout_candidates WHERE item_id = 'ITM-LATE-1';
  -- esperado: ('missing_stock_record', 'pending_grace')
  ```
  Produzir a posição de estoque do item; aguardar 1 refresh:
  ```sql
  SELECT count() FROM dh_marts.stockout_candidates WHERE item_id = 'ITM-LATE-1';
  -- esperado: 0, sem nenhuma intervenção manual
  ```
  Este é o critério que prova ADR-0004 §7 + ADR-0005 §2 na prática.

- [ ] **A7 — Delete na origem gera candidato.**
  Produzir `_op='d'` na posição de `('ITM-DISC-1','DC01')` (único CD do item):
  ```sql
  SELECT stockout_class FROM dh_marts.stockout_candidates WHERE item_id = 'ITM-DISC-1';
  -- esperado: 'missing_stock_record' (a view corrente esconde a linha deletada)
  ```

- [ ] **A8 — Supressão por atraso de tópico funciona.**
  Parar o connector do tópico de estoque por > 1 h (ou injetar `_kafka_ts` antigo em
  ambiente de teste) e forçar refresh:
  ```sql
  SELECT uniqExact(evidence_status) AS s, any(evidence_status) AS v
  FROM dh_marts.stockout_candidates
  WHERE stockout_class IN ('missing_stock_record','missing_dc_position');
  -- esperado: s = 1 e v = 'suppressed_topic_lag'
  ```

- [ ] **A9 — Nenhuma coluna monetária em `Float`.**
  ```sql
  SELECT name, type FROM system.columns
  WHERE database='dh_marts' AND table='stockout_candidates' AND type LIKE '%Float%';
  ```
  Esperado: vazio.

- [ ] **A10 — Refresh saudável e duração registrada.**
  ```sql
  SELECT status, last_refresh_result, last_success_time, last_success_duration_ms, exception
  FROM system.view_refreshes WHERE view = 'mv__stockout_candidates';
  ```
  Esperado: `Finished`, sem exceção, duração anotada em `docs/evaluation/`.

- [ ] **A11 — Complementaridade com o cenário 006.** Nenhum par que está no 006 está
  na classe (a) ou (c) do 007:
  ```sql
  SELECT count() FROM dh_marts.stockout_candidates AS s
  INNER JOIN dh_marts.stock_coverage_dc_item AS c
    ON s.item_id = c.item_id AND s.dc_id = c.dc_id
  WHERE s.stockout_class IN ('missing_stock_record','missing_dc_position');
  ```
  Esperado: `0`.

## Checks de qualidade associados

| `check_id` | Classe | Regra | Threshold inicial | Severidade |
|---|---|---|---|---|
| `dq.rel.stockout_missing_record_rate` | corretude | `confirmed` da classe (a) ÷ itens vendidos na janela | warn ≥ 0,005 · error ≥ 0,02 | error |
| `dq.rel.stockout_evidence_age_p99` | corretude | `quantile(0.99)(evidence_age_seconds)` das classes (a)/(c) `pending_grace` | < 72 h (= carência) | error |
| `dq.marts.stockout.class_exclusive` | corretude | A1 (exclusividade) | 0 violações | error |
| `dq.marts.stockout.anti_join_parity` | corretude | A2 e A3 | delta = 0 | error |
| `dq.marts.stockout.complement_006` | corretude | A11 | 0 | error |
| `dq.marts.refresh_health` | liveness | A10 para esta MV | `last_success` < 30 min | error |
| `dq.landing.freshness[inventory.stock_position]` | liveness | `inventory_lag_seconds` | warn ≥ 300 s · error ≥ 1800 s | error |
| `dq.money.no_float` | corretude | A9 | 0 colunas `Float*` | error |

**Interpretação de `dq.rel.stockout_evidence_age_p99`:** se ele estourar 72 h, a
conclusão **não** é "afrouxar a carência". É uma de duas: o cadastro de estoque tem
buraco real (incidente de negócio), ou a janela de *late arrival* do ADR-0004 §7
está subdimensionada para a origem real (incidente de arquitetura, que exige ADR
novo ajustando a janela **e** esta carência juntas).

## Consumo

**L3 — views de recorte:**

```sql
-- internal/contexts/analytics/sql/40-reports/0070__v_stockout.sql

-- fila operacional: só o que é acionável
CREATE VIEW IF NOT EXISTS dh_reports.v_stockout_action_queue AS
SELECT stockout_class, item_id, dc_id, severity, units_sold_7d, revenue_at_risk,
       available, last_sale_at, evidence_age_seconds, refreshed_at
FROM dh_marts.stockout_candidates
WHERE evidence_status = 'confirmed'
ORDER BY severity DESC, revenue_at_risk DESC;

-- resumo analítico: agregação SEMPRE por classe (RN-6)
CREATE VIEW IF NOT EXISTS dh_reports.v_stockout_summary AS
SELECT
    stockout_class,
    evidence_status,
    count()                      AS candidates,
    uniqExact(item_id)           AS items,
    uniqExact(dc_id)             AS dcs,
    sum(units_sold_7d)           AS units_sold_7d,
    sum(revenue_at_risk)         AS revenue_at_risk,
    max(evidence_age_seconds)    AS max_evidence_age_seconds,
    any(refreshed_at)            AS refreshed_at
FROM dh_marts.stockout_candidates
GROUP BY stockout_class, evidence_status;
```

**API (ADR-0011 §5):**

| Endpoint | Fonte | Frescor declarado | Role |
|---|---|---|---|
| `GET /reports/stockout/queue?dc_id=&severity=` | `dh_reports.v_stockout_action_queue` | ≤ 15 min | `dh_app` |
| `GET /reports/stockout/summary` | `dh_reports.v_stockout_summary` | ≤ 15 min | `dh_analyst` |

**Operacional:** a fila alimenta duas ações distintas — classe (a)/(c) `confirmed`
vai para o time de dados/cadastro (é um buraco de replicação ou de master data);
classe (b) vai para abastecimento (é falta de produto). A separação por classe é o
que torna o relatório acionável em vez de apenas alarmante.

## Arquivos no repositório

| Caminho | Conteúdo |
|---|---|
| `internal/contexts/analytics/sql/30-marts/0070__stockout_candidates.sql` | tabela + Refreshable MV |
| `internal/contexts/analytics/sql/40-reports/0070__v_stockout.sql` | views L3 |
| `internal/contexts/analytics/sql/40-reports/0070__dq_stockout.sql` | queries dos checks |
| `internal/contexts/analytics/reports/stockout.go` | queries versionadas + handlers |
| `test/e2e/stockout_test.go` | A6 (autocorreção), A7 (delete), A8 (supressão) |
| `docs/runbooks/stockout-triage.md` | o que fazer com cada classe `confirmed` |
| `docs/scenarios/007-stockout-anti-join.md` | este documento |
