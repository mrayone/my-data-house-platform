# ADR-0005 — Escolher entre MV incremental, Refreshable MV, Dictionary e JOIN em query por uma árvore de decisão fixa

- **Status:** Accepted
- **Data:** 2026-09-13
- **Decisores:** Engenharia de Dados
- **ADRs relacionados:** 0001, 0004, 0011

## Contexto

O requisito é *"agregar informações que sirvam para aplicações e também dados
analíticos"*, cruzando ~30 tópicos onde **alguns se conectam por ID, outros por
coluna qualquer, e outros não se conectam**.

O erro que este ADR existe para impedir:

> **Materialized View incremental no ClickHouse não é join de streams.**

Ela é um **trigger de INSERT** na tabela declarada no `FROM`. Consequências
concretas, todas capazes de produzir número errado de forma silenciosa:

1. Ela só dispara quando **aquela** tabela recebe insert. Insert no lado direito
   de um `JOIN` dentro da MV **não dispara nada** — aquele resultado nunca é
   recalculado.
2. Ela só vê o **bloco inserido**, não a tabela. `GROUP BY` dentro da MV agrega o
   bloco; a consolidação final depende do engine do destino.
3. Um `JOIN` dentro da MV lê o lado direito **no instante do insert**. Com CDC
   fora de ordem (ADR-0004), o lado direito pode estar vazio — e o resultado
   errado **fica gravado para sempre**.

Portanto "usar materialized view para agregar" não é uma resposta: é preciso uma
regra que diga *qual* mecanismo para *qual* forma de conexão.

## Decisão

### Árvore de decisão (normativa)

```
A transformação envolve UMA única tabela de origem e é linha-a-linha?
├─ SIM ─> MV INCREMENTAL (L0 -> L1). Padrão para toda entidade.
└─ NÃO
   │
   É agregação de UM fato APPEND-ONLY e IMUTÁVEL (nunca sofre UPDATE de CDC)?
   ├─ SIM ─> MV INCREMENTAL -> AggregatingMergeTree / SummingMergeTree
   └─ NÃO
      │
      A tabela do outro lado é PEQUENA (< ~10M linhas), de cadastro, e a
      correlação é lookup por chave exata?
      ├─ SIM ─> DICTIONARY + dictGet() (em query ou dentro de MV incremental)
      └─ NÃO
         │
         A correlação é entre 2+ entidades MUTÁVEIS, ou por intervalo/temporal,
         ou depende de dado que pode chegar depois?
         ├─ SIM ─> REFRESHABLE MATERIALIZED VIEW (recalcula, se autocorrige)
         └─ NÃO ─> VIEW + JOIN EM TEMPO DE QUERY (L3)
```

### Os quatro mecanismos

#### 1. MV incremental — transformação e agregado aditivo

Uso permitido, **e somente estes**:
- **L0 → L1**: normalização de linha única, sem `JOIN`, sem subquery em outra
  tabela. Aplica `_is_deleted` a partir de `_op`.
- **Fato imutável → agregador**: por exemplo contagem de eventos por dia a partir
  de um tópico append-only.
- **Enriquecimento por `dictGet()`**: permitido, pois o dictionary é lookup em
  memória e não é um join de streams. Aceita-se que o valor gravado é o vigente
  no momento do insert (é *point-in-time*, e isso deve ser intencional).

Proibido: `JOIN` com outra tabela de fato; agregação sobre entidade mutável
(ADR-0004 §6).

```sql
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_core.mv__sales__order_raw__to__order
TO dh_core.sales__order AS
SELECT
    order_id, customer_id, business_unit_code, order_status, currency,
    total_amount, created_at, updated_at,
    _cdc_seq,
    if(_op = 'd', 1, 0) AS _is_deleted,
    _ingested_at
FROM dh_landing.sales__order_raw;
```

#### 2. Refreshable MV — correlação entre entidades mutáveis

É o mecanismo **padrão para os marts** (`dh_marts`), porque é o único que
**se autocorrige** quando o dado do outro lado chega depois.

```sql
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_marts.mv__order_360
REFRESH EVERY 1 MINUTE APPEND      -- ou substituição total, conforme o mart
TO dh_marts.order_360 AS
SELECT ...
FROM dh_core.v_sales__order_current o
LEFT JOIN dh_core.v_sales__order_item_current i USING (order_id)
...
```

Regras de uso:
- Sempre lê das views `v_<entidade>_current` (ADR-0004 §4), nunca das tabelas.
- Intervalo de refresh declarado no doc do mart, justificado pelo requisito de
  frescor daquele relatório. Padrão: 1 min para marts aplicacionais, 15 min para
  analíticos amplos.
- Marts grandes recalculam **por janela** (últimos N dias, padrão 3) e não a
  tabela inteira; o histórico fora da janela é considerado estável. A janela é a
  tolerância a *late arrival* do ADR-0004 §7.
- `Refreshable MV` é feature com *setting* próprio; habilitado em
  `deploy/clickhouse/config/` (`allow_experimental_refreshable_materialized_view`
  quando a versão exigir). A versão usada e o estado da flag ficam registrados em
  `docs/evaluation/` porque **isso é um item de avaliação do ClickHouse Cloud**.

#### 3. Dictionary — cadastro pequeno, lookup por chave

Para `business_unit` e `discount_codes` (poucos milhares de linhas, consultados
em quase todo relatório):

```sql
CREATE DICTIONARY IF NOT EXISTS dh_core.dict__business_unit
(
    bu_code String, bu_name String, channel String, region String
)
PRIMARY KEY bu_code
SOURCE(CLICKHOUSE(QUERY 'SELECT bu_code,bu_name,channel,region FROM dh_core.v_organization__business_unit_current'))
LAYOUT(COMPLEX_KEY_HASHED())
LIFETIME(MIN 300 MAX 600);
```

Por que dictionary e não join: elimina o join do plano, cabe em memória,
`dictGet` é O(1), e o `LIFETIME` cuida da atualização. É a resposta para
*"tópicos que só trazem informação de cadastro"*.

Limite: se o cadastro crescer além de ~10M linhas ou a memória do nó apertar,
volta a ser join. Monitorar `system.dictionaries.bytes_allocated` (ADR-0010).

#### 4. JOIN em tempo de query — o resto

Para correlação ad-hoc, exploratória, ou de baixa frequência. Vive em
`dh_reports` como `VIEW` ou *parameterized view*. Inclui os casos que os outros
três não cobrem bem:

- **`ASOF JOIN`** para conexão **temporal/por intervalo** — o caso de `prices`:
  o preço vigente na data do pedido não é lookup por ID, é "a última versão do
  preço do item cuja vigência começou antes do pedido". Nenhum agregador
  incremental expressa isso.
  ```sql
  SELECT i.order_id, i.item_id, i.unit_price, p.list_price
  FROM dh_core.v_sales__order_item_current i
  ASOF LEFT JOIN dh_core.v_pricing__prices_current p
    ON i.item_id = p.item_id AND i.created_at >= p.valid_from
  ```
- **anti-join** (`LEFT JOIN ... WHERE right.key = ''` ou `NOT IN`) para achar o
  que **não** se conecta — vendas sem posição de estoque, item sem preço. É como
  a ausência de relação se torna relatório (cenários 007 e 009).
- **`GLOBAL JOIN`** só em cluster; na PoC single-node é irrelevante, mas o SQL
  deve ser escrito de modo que a migração para cluster no ClickHouse Cloud não
  exija reescrita. Registrado como item de avaliação.

### Tabela de decisão por entidade da PoC

| Entidade | Natureza | Mecanismo para L1 | Participação em marts |
|---|---|---|---|
| `order` | mutável (status muda) | MV incremental L0→L1 | Refreshable MV |
| `order_item` | mutável | MV incremental L0→L1 | Refreshable MV (agregação por pedido) |
| `order_payment` | mutável (status de captura) | MV incremental L0→L1 | Refreshable MV |
| `customer` | mutável, cadastro médio | MV incremental L0→L1 | Refreshable MV + dictionary se couber |
| `business_unit` | cadastro pequeno, raramente muda | MV incremental L0→L1 | **Dictionary** |
| `discount_codes` | cadastro pequeno | MV incremental L0→L1 | **Dictionary** (join por código, não por ID) |
| `prices` | versionado por vigência | MV incremental L0→L1 | **ASOF JOIN** em query / Refreshable MV |
| `stock_position` | mutável, alta taxa | MV incremental L0→L1 | Refreshable MV + anti-join |

## Alternativas consideradas

### A) Só MV incremental para tudo
- **Prós:** menor latência possível; um mecanismo só para aprender.
- **Contras:** produz número errado nos casos de correlação e de entidade
  mutável, pelas três razões do Contexto. Erro silencioso e permanente.
- **Por que não:** inviável por corretude. Este é o ponto central do ADR.

### B) Só JOIN em tempo de query (sem materializar nada)
- **Prós:** sempre correto e fresco; zero storage extra; zero pipeline.
- **Contras:** o caso aplicacional ("tudo do pedido X em ms") pagaria join de 6
  entidades a cada request; e o analítico varreria tudo a cada relatório.
- **Por que não:** não atende latência. Mantido para o que é ad-hoc.

### C) Tabela desnormalizada única mantida por MV incremental com JOIN
- **Prós:** leitura trivial e rapidíssima.
- **Contras:** é exatamente o antipadrão do Contexto §3 — com CDC fora de ordem,
  grava errado e nunca corrige.
- **Por que não:** o resultado desnormalizado é desejável, mas quem o mantém deve
  ser **Refreshable MV**, não MV incremental com join. É o desenho do
  `dh_marts.order_360`.

### D) Motor de transformação externo agendado (dbt/Airflow)
- **Prós:** testes, lineage, dependências explícitas.
- **Contras:** componente e custo a mais, e a PoC quer medir o ClickHouse
  isolado. Refreshable MV cobre o mesmo papel dentro do banco.
- **Por que não agora:** registrado em `docs/evaluation/` como caminho de
  evolução se a Refreshable MV mostrar limites de orquestração.

## Consequências

### Positivas
- A pergunta "como agrego isso?" tem resposta determinística e revisável.
- Correção automática de late arrival sem código de compensação.
- Cadastro sai do plano de join, o que derruba latência dos relatórios.
- `ASOF JOIN` e anti-join viram recurso de primeira classe, cobrindo os "tópicos
  que se conectam por outra coluna" e os "que não se conectam".

### Negativas / custo aceito
- Quatro mecanismos = mais superfície conceitual. Mitigado: a árvore de decisão é
  normativa e o doc de cada mart declara qual mecanismo usou e por quê.
- Refreshable MV troca latência por corretude. Assumido e medido.
- Refresh por janela assume estabilidade fora da janela; correção retroativa
  antiga exige refresh manual (alvo `make mart-refresh MART=... WINDOW=...`).
- `dictGet` dentro de MV incremental grava valor *point-in-time*: correto para
  "qual era a BU no momento do pedido", errado se o requisito for "BU atual".
  Cada uso declara a intenção no doc do mart.

### Riscos e mitigação
| Risco | Mitigação |
|---|---|
| Alguém escrever `JOIN` em MV incremental | Check estático em `make verify` bloqueia `JOIN` em `CREATE MATERIALIZED VIEW` sem `REFRESH` |
| Refreshable MV indisponível/limitada na versão ou no Cloud | Item explícito de `docs/evaluation/`; fallback = tabela + `INSERT ... SELECT` agendado por `dhctl` |
| Dictionary estourando memória | Métrica de `bytes_allocated` + limite documentado |
| Janela de refresh curta demais perdendo late arrival | Métrica de idade máxima de órfão (ADR-0010); janela ≥ p99 do atraso observado |

## Impacto no repositório

- `internal/contexts/<ctx>/sql/20-core/` — MVs incrementais + `v_*_current`.
- `internal/contexts/<ctx>/sql/30-marts/` e `internal/contexts/analytics/sql/30-marts/`
  — Refreshable MVs e dictionaries.
- `internal/contexts/*/sql/40-reports/` — views e parameterized views.
- `scripts/checks/no-join-in-incremental-mv.sh`.
- Todo mart tem doc em `docs/scenarios/` declarando mecanismo, intervalo de
  refresh e janela.

## Como validar

```sql
-- nenhuma MV incremental com JOIN
SELECT name FROM system.tables
WHERE engine='MaterializedView'
  AND create_table_query ILIKE '% JOIN %'
  AND create_table_query NOT ILIKE '%REFRESH%';
-- esperado: vazio

-- refreshable MVs estão saudáveis
SELECT view, status, last_success_time, last_refresh_result, exception
FROM system.view_refreshes;

-- dictionaries carregados
SELECT name, status, element_count, bytes_allocated, last_exception
FROM system.dictionaries;
```

Teste de autocorreção (Fase 06, e2e): produzir `order_item` **antes** do `order`,
confirmar que `dh_marts.order_360` fica incompleto no primeiro refresh e
**completo** depois do refresh seguinte à chegada do `order`, sem intervenção.
