# Cenário 010 — Catálogo parado: itens com preço e estoque e nenhuma venda

- **Perfil:** analítico
- **Pergunta de negócio:** "Quanto capital está imobilizado em itens que têm preço cadastrado e estoque disponível, mas não vendem? Há quanto tempo?"
- **Mecanismo:** **Refreshable MV** com substituição total (ADR-0005) — correlação de duas entidades que **não passam pela entidade transacional**, mais um anti-join contra vendas
- **Frescor esperado:** ≤ 65 min (`REFRESH EVERY 60 MINUTE`)
- **Janela de recálculo:** snapshot completo do catálogo; a janela existe na **demanda** (90 dias)
- **Entidades envolvidas:** `pricing.prices`, `inventory.stock_position`, `sales.order_item`, `sales.order`
- **Tipo de correlação:** **`loose`** — `prices` e `stock_position` se ligam **apenas** por `item_id`, sem hierarquia nem entidade intermediária; a venda entra por **ausência** (anti-join)
- **ADRs relevantes:** ADR-0001, ADR-0004 (§4, §7), ADR-0005 (§4), ADR-0010, ADR-0011

---

## Por que este cenário existe na PoC

O escopo da PoC diz que *"outros tópicos apenas informações que se conectam com
alguma outra coluna de alguma forma"*. Este é esse caso, na sua forma mais pura:

- `prices` e `stock_position` **não têm relação de negócio entre si.** Nenhum dos
  dois é pai do outro. Nenhum aponta para o outro. A única coisa que compartilham é
  uma coluna chamada `item_id`, e nem isso está garantido pela origem — os dois
  vêm de tabelas SAP diferentes.
- A informação de negócio **surge do cruzamento**, e o sinal mais valioso é a
  **ausência de um terceiro** (`order_item`).

Nos contratos, essa relação é declarada com `kind: loose` exatamente para não ser
confundida com `foreign_key`: órfão aqui não é defeito.

Prova três coisas:

1. **Que o ClickHouse gera informação de negócio de tópicos que não se conectam
   por chave de domínio.** Se a plataforma só soubesse cruzar por FK, metade dos 30
   tópicos do cenário real seria inútil.
2. **Que `loose` e `foreign_key` produzem checks de qualidade diferentes.** A mesma
   ausência é erro num caso e informação no outro; a plataforma tem de distinguir
   pelo contrato, não pelo julgamento de quem escreve a query.
3. **Que anti-join sobre catálogo inteiro é viável em custo.** Aqui o denominador é
   o catálogo (potencialmente milhões de SKUs), não a janela de vendas — é o
   anti-join mais caro da PoC, e o número vai para a avaliação.

---

## Fontes

| Tabela | Objeto lido | Camada | Por que este e não outro |
|---|---|---|---|
| Preço vigente | `dh_core.v_pricing__prices_current` | L1 | Define "item cadastrado com preço". Precisa da view: as N vigências da mesma chave precisam estar deduplicadas antes de escolher a corrente. |
| Estoque | `dh_core.v_inventory__stock_position_current` | L1 | Define "item com estoque". Entidade de alta taxa: sem `FINAL`, uma posição antiga poderia ser lida como atual. |
| Vendas | `dh_core.v_sales__order_item_current` | L1 | Entra **só** para provar a ausência. Agregado por `item_id` na janela de 90 dias, antes do anti-join. |
| Pedido | `dh_core.v_sales__order_current` | L1 | Filtra pedidos cancelados — venda cancelada não conta como venda. |

**O `FROM` é o catálogo, não a venda.** Essa é a inversão que o cenário exige: quem
começa pelas vendas nunca encontra o que não vendeu.

---

## Modelo de saída

```sql
-- internal/contexts/analytics/sql/30-marts/0080__idle_catalog_items.sql

CREATE TABLE IF NOT EXISTS dh_marts.idle_catalog_items {ON_CLUSTER}
(
    item_id                  String,
    dc_id                    LowCardinality(String),

    -- lado pricing
    has_price                UInt8,
    price_list_id            LowCardinality(String),
    currency                 LowCardinality(String),
    list_price               Decimal(18,4),
    cost_price               Decimal(18,4),
    price_valid_from         Nullable(DateTime64(3)),

    -- lado inventory
    has_stock_record         UInt8,
    on_hand                  Int64,
    reserved                 Int64,
    available                Int64,
    safety_stock             Int64,
    position_at              Nullable(DateTime64(3)),

    -- lado vendas (ausência é o sinal)
    units_sold_90d           Int64,
    orders_90d               UInt64,
    last_sale_at             Nullable(DateTime64(3)),
    days_since_last_sale     Nullable(Int32),

    -- resultado
    idle_class               LowCardinality(String),  -- ver regra 4
    immobilized_capital      Decimal(38,4),           -- available * cost_price
    potential_revenue        Decimal(38,4),           -- available * list_price

    _refreshed_at            DateTime64(3)
)
ENGINE = ReplacingMergeTree(_refreshed_at)
PARTITION BY cityHash64(item_id) % 16
ORDER BY (item_id, dc_id);

-- projection para o corte gerencial: ranking de capital imobilizado por CD.
-- Justificativa (ADR-0011 §2): a tabela é ORDER BY item_id para lookup de SKU,
-- mas o consumo dominante é "maiores ofensores por CD", que faria full scan.
ALTER TABLE dh_marts.idle_catalog_items {ON_CLUSTER}
ADD PROJECTION IF NOT EXISTS p_by_dc_capital
( SELECT * ORDER BY (dc_id, idle_class, immobilized_capital) );
```

Decisões do DDL:

- **`PARTITION BY cityHash64(item_id) % 16`** e não por data: o mart é um snapshot
  do catálogo, sem eixo temporal natural. Particionar por hash permite recálculo
  paralelo por faixa se o volume exigir (a saída documentada no cenário 008), e
  mantém as partições de tamanho parecido.
- **`has_price` e `has_stock_record` explícitos**: a combinação
  (`tem preço`, `tem estoque`, `vendeu`) é o produto deste mart. Deduzir isso de
  valores zerados no consumo convidaria a interpretações divergentes.
- **`idle_class` materializada**: a classificação é regra de negócio (regra 4) e
  precisa ser única para todos os consumidores.
- **`days_since_last_sale` `Nullable`**: item que nunca vendeu não tem "dias desde
  a última venda". `0` diria "vendeu hoje"; um número grande arbitrário poluiria
  qualquer média.
- **`immobilized_capital` com `available` negativo** não é zerado — ver regra 8.

---

## Transformação

```sql
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_marts.mv__idle_catalog_items {ON_CLUSTER}
REFRESH EVERY 60 MINUTE APPEND
TO dh_marts.idle_catalog_items AS
WITH
    (now() - INTERVAL 90 DAY) AS demand_from,

    -- preço CORRENTE por item: a vigência mais recente que já começou.
    -- argMax sobre valid_from resolve sem ASOF, porque aqui não há data de
    -- referência variável — a referência é "agora".
    current_price AS (
        SELECT
            item_id,
            argMax(price_list_id, valid_from) AS price_list_id,
            argMax(currency,      valid_from) AS currency,
            argMax(list_price,    valid_from) AS list_price,
            argMax(cost_price,    valid_from) AS cost_price,
            max(valid_from)                   AS price_valid_from
        FROM dh_core.v_pricing__prices_current
        WHERE valid_from <= now()
          AND (valid_to IS NULL OR valid_to >= now())
        GROUP BY item_id
    ),

    -- demanda por item na janela, excluindo pedido cancelado
    demand AS (
        SELECT
            i.item_id                    AS item_id,
            sum(i.quantity)              AS units_sold_90d,
            countDistinct(i.order_id)    AS orders_90d,
            max(i.created_at)            AS last_sale_at
        FROM dh_core.v_sales__order_item_current AS i
        INNER JOIN dh_core.v_sales__order_current AS o USING (order_id)
        WHERE i.created_at >= demand_from
          AND o.order_status != 'canceled'
        GROUP BY i.item_id
    ),

    -- a espinha dorsal: FULL OUTER entre estoque e preço.
    -- Nem prices é pai de stock_position, nem o contrário (relation kind: loose),
    -- então nenhum dos dois pode ser o lado preservado exclusivo.
    catalog AS (
        SELECT
            if(s.item_id = '', p.item_id, s.item_id)          AS item_id,
            if(s.item_id = '', '(sem estoque)', s.dc_id)      AS dc_id,
            if(p.item_id = '', 0, 1)                          AS has_price,
            if(s.item_id = '', 0, 1)                          AS has_stock_record,
            p.price_list_id, p.currency, p.list_price, p.cost_price, p.price_valid_from,
            s.on_hand, s.reserved, s.available, s.safety_stock, s.position_at
        FROM dh_core.v_inventory__stock_position_current AS s
        FULL OUTER JOIN current_price AS p ON s.item_id = p.item_id
    )
SELECT
    c.item_id                                                  AS item_id,
    c.dc_id                                                    AS dc_id,

    c.has_price                                                AS has_price,
    if(c.has_price = 1, c.price_list_id, '(sem preco)')         AS price_list_id,
    if(c.has_price = 1, c.currency, '')                         AS currency,
    c.list_price                                               AS list_price,
    c.cost_price                                               AS cost_price,
    if(c.has_price = 1, c.price_valid_from, NULL)               AS price_valid_from,

    c.has_stock_record                                         AS has_stock_record,
    c.on_hand, c.reserved, c.available, c.safety_stock,
    if(c.has_stock_record = 1, c.position_at, NULL)             AS position_at,

    -- anti-join: d.item_id = '' significa "não vendeu na janela"
    coalesce(d.units_sold_90d, 0)                              AS units_sold_90d,
    coalesce(d.orders_90d, 0)                                  AS orders_90d,
    if(d.item_id = '', NULL, d.last_sale_at)                    AS last_sale_at,
    if(d.item_id = '', NULL,
       dateDiff('day', d.last_sale_at, now()))                  AS days_since_last_sale,

    -- classificação (regra 4)
    multiIf(
        c.has_price = 1 AND c.has_stock_record = 1 AND c.available > 0
            AND coalesce(d.units_sold_90d, 0) = 0,             'parado_com_estoque',
        c.has_price = 1 AND c.has_stock_record = 1 AND c.available <= 0
            AND coalesce(d.units_sold_90d, 0) = 0,             'parado_sem_estoque',
        c.has_price = 1 AND c.has_stock_record = 0
            AND coalesce(d.units_sold_90d, 0) = 0,             'preco_sem_posicao',
        c.has_price = 0 AND c.has_stock_record = 1 AND c.available > 0,
                                                                'estoque_sem_preco',
        coalesce(d.units_sold_90d, 0) > 0,                      'ativo',
                                                                'indefinido'
    )                                                          AS idle_class,

    if(c.has_stock_record = 1 AND c.has_price = 1,
       toDecimal128(c.available, 4) * c.cost_price,
       toDecimal128(0, 4))                                     AS immobilized_capital,
    if(c.has_stock_record = 1 AND c.has_price = 1,
       toDecimal128(c.available, 4) * c.list_price,
       toDecimal128(0, 4))                                     AS potential_revenue,

    now64(3)                                                   AS _refreshed_at

FROM catalog AS c
LEFT JOIN demand AS d ON c.item_id = d.item_id;
```

```sql
CREATE VIEW IF NOT EXISTS dh_marts.v_idle_catalog_items {ON_CLUSTER} AS
SELECT * EXCEPT (_refreshed_at) FROM dh_marts.idle_catalog_items FINAL;

-- internal/contexts/analytics/sql/40-reports/0080__idle_catalog.sql
CREATE VIEW IF NOT EXISTS dh_reports.v_immobilized_capital_by_dc {ON_CLUSTER} AS
SELECT dc_id,
       countDistinct(item_id)         AS items,
       sum(available)                 AS units,
       sum(immobilized_capital)       AS capital,
       sum(potential_revenue)         AS potential_revenue
FROM dh_marts.v_idle_catalog_items
WHERE idle_class = 'parado_com_estoque'
GROUP BY dc_id
ORDER BY capital DESC;

CREATE VIEW IF NOT EXISTS dh_reports.v_catalog_gaps {ON_CLUSTER} AS
SELECT idle_class, countDistinct(item_id) AS items, sum(available) AS units,
       sum(immobilized_capital) AS capital
FROM dh_marts.v_idle_catalog_items
WHERE idle_class IN ('estoque_sem_preco', 'preco_sem_posicao')
GROUP BY idle_class;
```

---

## Regras de negócio

1. **Grão:** `item_id` × `dc_id`. Item com preço e sem nenhuma posição de estoque
   aparece com `dc_id = '(sem estoque)'` — uma linha, não zero.
2. **"Preço vigente" é a versão com maior `valid_from <= now()` cujo `valid_to` é
   nulo ou futuro.** Diferente do cenário 005, aqui a referência temporal é
   **agora**, não a data do pedido — por isso `argMax`, não `ASOF JOIN`.
3. **"Não vendeu" é "zero unidades em pedidos não cancelados nos últimos 90 dias".**
   A janela é do relatório, não do dado: item que vendeu há 120 dias é parado.
4. **Classificação (`idle_class`), mutuamente exclusiva e avaliada nesta ordem:**

   | `idle_class` | Significado | Ação típica |
   |---|---|---|
   | `ativo` | vendeu na janela | nenhuma |
   | `parado_com_estoque` | tem preço, tem estoque disponível, não vendeu | liquidação, remanejamento |
   | `parado_sem_estoque` | tem preço, posição zerada ou negativa, não vendeu | ruptura prolongada ou descontinuado |
   | `preco_sem_posicao` | tem preço, **nenhuma** posição de estoque, não vendeu | cadastro incompleto ou item novo |
   | `estoque_sem_preco` | tem estoque disponível, **nenhum** preço vigente | **invendável** — lacuna de cadastro |
   | `indefinido` | resto | investigar |

   `estoque_sem_preco` é o achado mais acionável: capital parado que não pode nem
   ser vendido.
5. **A ausência de correspondência entre `prices` e `stock_position` não é erro.**
   A relação é `loose` no contrato; `dq.rel.orphan_rate` **não** se aplica a ela.
   O que se mede é `dq.catalog.coverage` — quanto do catálogo tem os dois lados.
6. **`FULL OUTER JOIN` e não `LEFT`.** Precisamos de item-com-preço-sem-estoque
   **e** item-com-estoque-sem-preço. Escolher um lado como base perderia metade dos
   achados, e a escolha seria arbitrária porque nenhum dos dois é pai do outro.
7. **Item sem preço não tem `immobilized_capital`** — o capital é zero porque não é
   calculável sem `cost_price`, não porque não exista. A coluna
   `has_price = 0` é o que informa isso, e o consumo precisa olhá-la.
8. **`available` negativo não é zerado.** É inconsistência conhecida da origem
   (documentada no contrato de `stock_position`) e gera capital negativo, o que é
   absurdo e portanto visível. Zerar esconderia o problema; o check
   `dq.stock.negative_available` o reporta.
9. **Moeda não é convertida nem somada entre moedas.** `currency` está na linha;
   qualquer soma de capital por CD deve filtrar ou agrupar por moeda. A view
   `v_immobilized_capital_by_dc` assume moeda única no ambiente da PoC e isso está
   declarado como limitação.
10. **Item apagado no SAP** desaparece do mart (as views `v_*_current` o excluem).
    Isso é correto: não há capital imobilizado em item que não existe mais.
11. **O mart não decide o que descontinuar.** Ele quantifica. A decisão depende de
    sazonalidade e curva de vida do produto, que não estão no escopo dos oito
    tópicos.

---

## Armadilhas

### 1. Começar o `FROM` pelas vendas
`FROM order_item LEFT JOIN prices ...` só enxerga itens que venderam — e o
relatório é justamente sobre os que **não** venderam. O `FROM` é o catálogo.

### 2. `LEFT JOIN` em vez de `FULL OUTER`
Perde metade dos achados, e qual metade depende de qual lado foi escolhido como
base. Como a relação é `loose` (nenhum é pai), não existe base "natural".

### 3. `NULL` vs `''` no `OUTER JOIN`
Mesma armadilha do cenário 009, e aqui em duas juntas ao mesmo tempo. Coluna
não-`Nullable` do lado ausente vem como valor padrão do tipo (`''`, `0`), não
`NULL`. Todos os testes de ausência usam `= ''`, e é por isso que `has_price` e
`has_stock_record` existem como colunas explícitas: uma vez calculados
corretamente, ninguém mais precisa acertar essa comparação.

### 4. Confundir `available = 0` com "sem registro de estoque"
São diagnósticos diferentes: posição zerada é ruptura; ausência de posição é
cadastro. As classes `parado_sem_estoque` e `preco_sem_posicao` separam os dois, e
misturá-las manda o time errado investigar.

### 5. Ler `prices` sem dedup antes do `argMax`
`prices` é `ReplacingMergeTree`; sem `FINAL`, `argMax(list_price, valid_from)`
pode escolher o valor de uma versão já corrigida da mesma vigência.

### 6. Aplicar `dq.rel.orphan_rate` a uma relação `loose`
Alarmaria constantemente sobre algo que é esperado, e treinaria o time a ignorar o
alerta. A distinção vem do `kind` do contrato — é o motivo de `loose` existir como
valor válido.

### 7. Custo do anti-join sobre o catálogo inteiro
O denominador aqui é o catálogo, não a janela de vendas. É o refresh mais caro da
PoC. Por isso o intervalo é de 60 min e a partição é por hash — e por isso a
duração precisa ir para `docs/evaluation/results.md`.

### 8. Somar capital entre moedas
Ver regra 9. A view de consumo da PoC assume moeda única e declara isso.

---

## Critérios de aceite

- [ ] **Item com preço e estoque que nunca vendeu é encontrado**:
  ```sql
  SELECT idle_class, available, immobilized_capital, days_since_last_sale
  FROM dh_marts.v_idle_catalog_items WHERE item_id = 'SKU-PARADO';
  -- esperado: idle_class='parado_com_estoque', immobilized_capital > 0,
  --           days_since_last_sale = NULL
  ```
- [ ] **Item com estoque e sem preço é encontrado** (o achado mais acionável):
  ```sql
  SELECT idle_class, has_price, has_stock_record, available
  FROM dh_marts.v_idle_catalog_items WHERE item_id = 'SKU-SEM-PRECO-COM-ESTOQUE';
  -- esperado: idle_class='estoque_sem_preco', has_price=0, has_stock_record=1
  ```
- [ ] **Item com preço e sem nenhuma posição é encontrado** (prova o `FULL OUTER`):
  ```sql
  SELECT idle_class, dc_id, has_stock_record
  FROM dh_marts.v_idle_catalog_items WHERE item_id = 'SKU-PRECO-SEM-POSICAO';
  -- esperado: idle_class='preco_sem_posicao', dc_id='(sem estoque)', has_stock_record=0
  ```
- [ ] **Item que vendeu sai da lista de parados**: produzir venda de `SKU-PARADO` e
  aguardar refresh.
  ```sql
  -- esperado: idle_class muda para 'ativo', days_since_last_sale = 0
  ```
- [ ] **Venda cancelada não reativa o item**: produzir venda e depois cancelar o
  pedido.
  ```sql
  SELECT idle_class, units_sold_90d FROM dh_marts.v_idle_catalog_items
  WHERE item_id = 'SKU-VENDA-CANCELADA';
  -- esperado: volta a 'parado_com_estoque', units_sold_90d = 0
  ```
- [ ] **Venda antiga (120 dias) não conta como atividade**:
  ```sql
  SELECT idle_class, units_sold_90d, days_since_last_sale
  FROM dh_marts.v_idle_catalog_items WHERE item_id = 'SKU-VENDA-ANTIGA';
  -- esperado: idle_class='parado_com_estoque', units_sold_90d = 0,
  --           days_since_last_sale = 120  (a data é conhecida, a janela não a inclui)
  ```
- [ ] **Preço vigente correto**: cadastrar duas vigências (`valid_from` D-30 = 100,
  D-5 = 80) e uma futura (D+5 = 60).
  ```sql
  SELECT list_price FROM dh_marts.v_idle_catalog_items WHERE item_id = 'SKU-VIGENCIA';
  -- esperado: 80.0000  (nem 100, nem o preço futuro de 60)
  ```
- [ ] **Preço expirado não é usado**: vigência com `valid_to` no passado.
  ```sql
  SELECT has_price, idle_class FROM dh_marts.v_idle_catalog_items
  WHERE item_id = 'SKU-PRECO-EXPIRADO';
  -- esperado: has_price = 0 e classe de lacuna de cadastro
  ```
- [ ] **Cobertura do catálogo fecha**:
  ```sql
  SELECT countDistinct(item_id) FROM dh_marts.v_idle_catalog_items;
  -- esperado: igual a countDistinct(item_id) da UNIÃO de
  --   v_pricing__prices_current (vigente) e v_inventory__stock_position_current
  ```
- [ ] **Classes são mutuamente exclusivas**:
  ```sql
  SELECT count() FROM (SELECT item_id, dc_id, countDistinct(idle_class) c
    FROM dh_marts.v_idle_catalog_items GROUP BY 1,2 HAVING c > 1);
  -- esperado: 0
  SELECT count() FROM dh_marts.v_idle_catalog_items WHERE idle_class = 'indefinido';
  -- esperado: 0 em dados saudáveis; > 0 é gap na regra 4
  ```
- [ ] **`available` negativo visível, não escondido**:
  ```sql
  SELECT count() FROM dh_marts.v_idle_catalog_items WHERE immobilized_capital < 0;
  -- esperado: igual ao número de posições com available < 0 (reportado, não zerado)
  ```
- [ ] **Projection usada no ranking por CD**: `EXPLAIN indexes = 1` de
  `v_immobilized_capital_by_dc` mostra `p_by_dc_capital`.
- [ ] **Duração do refresh registrada** em `docs/evaluation/results.md`, com o
  tamanho do catálogo do dataset de benchmark.

---

## Checks de qualidade associados

| check_id | Regra | Severidade |
|---|---|---|
| `dq.catalog.coverage` | **novo** — % de itens com preço **e** posição de estoque > 90% | warn |
| `dq.catalog.stock_without_price` | **novo** — itens `estoque_sem_preco` < 1% do catálogo | error |
| `dq.catalog.class_exhaustive` | **novo** — nenhum item com `idle_class = 'indefinido'` | error |
| `dq.stock.negative_available` | **novo** — `available < 0` em menos de 0,1% das posições | warn |
| `dq.marts.refresh_health` | refresh sem exceção, `last_success` < 3 h | error |
| `dq.marts.refresh_duration` | duração < 50% do intervalo (60 min) | warn |
| `dq.money.no_float` | colunas monetárias em `Decimal` | error |

**`dq.rel.orphan_rate` não se aplica** à relação `prices ↔ stock_position`: ela é
`loose`, e a ausência é o produto. O gerador de checks lê o `kind` do contrato para
decidir isso — ver [ADR-0003](../adr/0003-extensibilidade-contract-first.md).

---

## Consumo

```sql
-- maiores ofensores: capital parado por CD e classe
SELECT dc_id, idle_class,
       countDistinct(item_id)    AS items,
       sum(available)            AS units,
       sum(immobilized_capital)  AS capital,
       sum(potential_revenue)    AS potential_revenue
FROM dh_marts.v_idle_catalog_items
WHERE idle_class != 'ativo' AND currency = 'BRL'
GROUP BY dc_id, idle_class
ORDER BY capital DESC
LIMIT 50;
```

```sql
-- lacunas de cadastro, para o time de dados mestres
SELECT * FROM dh_reports.v_catalog_gaps;
```

```sql
-- top 100 SKUs parados com maior capital, para o time comercial
SELECT item_id, dc_id, available, cost_price, immobilized_capital,
       days_since_last_sale
FROM dh_marts.v_idle_catalog_items
WHERE idle_class = 'parado_com_estoque'
ORDER BY immobilized_capital DESC
LIMIT 100;
```

Sem endpoint aplicacional: o consumo é gerencial e o perfil de query não cabe no
`max_execution_time = 3` do role `dh_app` (ADR-0011 §4).

---

## Arquivos no repositório

| Caminho | Conteúdo |
|---|---|
| `internal/contexts/analytics/sql/30-marts/0080__idle_catalog_items.sql` | tabela, projection, Refreshable MV, view |
| `internal/contexts/analytics/sql/40-reports/0080__idle_catalog.sql` | `v_immobilized_capital_by_dc`, `v_catalog_gaps` |
| `internal/contexts/analytics/sql/40-reports/0081__dq_catalog.sql` | `dq.catalog.*`, `dq.stock.negative_available` |
| `internal/contexts/analytics/reports/idle_catalog.go` | queries nomeadas |
| `internal/contexts/pricing/generator/prices.go` | SKU sem preço, preço expirado, vigência futura |
| `internal/contexts/inventory/generator/stock.go` | SKU sem posição, `available` negativo, posição zerada |
| `test/e2e/idle_catalog_test.go` | as seis combinações de classe, venda cancelada, venda antiga, vigência |
