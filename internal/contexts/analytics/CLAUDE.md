# CLAUDE.md — contexto `analytics`

## O que é

O contexto **sem tópico próprio**. Aqui vivem os marts e relatórios que **cruzam
contextos** — e é a resposta à pergunta "de qual contexto é o `order_360`?".

Existe por desenho ([ADR-0009](../../../docs/adr/0009-layout-de-pastas-context-first.md)
regras 3 e 4): SQL de um contexto só referencia objetos daquele contexto. Cruzou
contexto, vem para cá.

## Regra de ordenação

O runner de migrations ordena `analytics` **por último** dentro de cada camada, em
ordem alfabética invertida à regra geral — porque seus marts dependem do core de
todos os outros ([ADR-0007](../../../docs/adr/0007-migrations-sql-versionado.md)).

## O que tem aqui

| Camada | Conteúdo |
|---|---|
| `sql/10-landing/` | **vazia.** Este contexto não tem tópico |
| `sql/20-core/` | **vazia.** Não há entidade própria |
| `sql/30-marts/` | os marts cross-context |
| `sql/40-reports/` | views de leitura e queries de check de qualidade |
| `reports/` | queries nomeadas em Go, consumidas pela API e pelo `dhctl reports` |
| `generator/` | **vazia.** Não produz carga |

## Marts deste contexto

| Mart | Cenário | Cruza | Mecanismo | Fase |
|---|---|---|---|---|
| `order_360` | [001](../../../docs/scenarios/001-order-360.md) | sales, customer, organization, pricing | Refreshable MV 1 min, janela 3 d | 06 |
| `revenue_by_bu_day` | [002](../../../docs/scenarios/002-revenue-by-bu-day.md) | sales, organization | Refreshable MV 15 min, janela 3 d | 06 |
| `payment_funnel_day` | [003](../../../docs/scenarios/003-payment-funnel.md) | sales, organization | Refreshable MV 5 min, janela 3 d | 06 |
| `discount_effectiveness` + `discount_baseline_day` | [004](../../../docs/scenarios/004-discount-effectiveness.md) | sales, pricing, organization | Refreshable MV 15 min | 06 |
| `item_margin_daily` | [005](../../../docs/scenarios/005-real-margin-vs-price-list.md) | sales, pricing | Refreshable MV 15 min + **`ASOF JOIN`**, janela 7 d | 06 |
| `stock_coverage_dc_item` | [006](../../../docs/scenarios/006-stock-coverage-vs-demand.md) | inventory, sales | Refreshable MV 15 min | 07 |
| `stockout_candidates` | [007](../../../docs/scenarios/007-stockout-anti-join.md) | inventory, sales | Refreshable MV 15 min, **anti-join** | 07 |
| `customer_metrics` + `customer_cohort_month` | [008](../../../docs/scenarios/008-customer-metrics.md) | customer, sales | Refreshable MV 30 min, **sem janela** + projection | 07 |
| `data_quality_overview` + `orphan_summary` + `orphan_records` | [009](../../../docs/scenarios/009-data-quality-orphans.md) | **todos** | Refreshable MV 5 min, **SQL gerado** | 07 |
| `idle_catalog_items` | [010](../../../docs/scenarios/010-catalog-without-sales.md) | pricing, inventory, sales | Refreshable MV 60 min, **`FULL OUTER`** + projection | 07 |

## Regras

1. **Toda leitura de L1 é pela view `dh_core.v_<ctx>__<entidade>_current`.** As
   únicas exceções são as duas do cenário 009, anotadas com
   `-- dq-exception: reads core table intentionally`, porque lá o objeto de medição
   **é** o estado físico. `no-direct-core-read.sh` respeita esse marcador.
2. **Escolha o mecanismo pela árvore do
   [ADR-0005](../../../docs/adr/0005-estrategia-de-agregacao-mv-refreshable-dictionary.md)**,
   não por hábito. MV incremental com `JOIN` é proibida e verificada.
3. **Agregue o lado 1:N antes do join.** `order LEFT JOIN order_item` multiplica o
   cabeçalho pelo número de itens.
4. **Destino é `ReplacingMergeTree(_refreshed_at)`** nos marts recalculados por
   janela, e a leitura é por uma view com `FINAL`. `SummingMergeTree` somaria o
   recálculo ao valor anterior.
5. **`Decimal(38,4)` em agregado monetário.** `sum()` de `Decimal(18,4)` sobre
   milhões de linhas estoura.
6. **Razões nunca são somadas nem tiradas pela média.** Taxas e percentuais são
   recalculados da razão das somas — no mart e no consumo.
7. **Toda coluna não-somável entre linhas está declarada** no doc do cenário
   (`countDistinct`, razões). O consumidor precisa saber.
8. **Todo mart tem doc de cenário** com mecanismo, intervalo, janela, regras de
   negócio, armadilhas e critérios de aceite verificáveis. Mart sem doc não entra.
9. **Máximo duas projections por tabela**, cada uma justificada no doc
   ([ADR-0011](../../../docs/adr/0011-serving-aplicacional-e-analitico-no-mesmo-store.md) §2).
10. **`ORDER BY` pelo perfil de acesso dominante**, não pelo que parece natural.

## Armadilhas que já custaram caro aqui

| Armadilha | Onde aparece |
|---|---|
| agregador incremental sobre entidade mutável (dupla contagem) | 002, 004, 008 |
| ler a tabela core sem `FINAL` | todos |
| join com 1:N sem agregar antes | 001, 002, 003, 008 |
| `IS NULL` em vez de `= ''` para detectar ausência em `LEFT`/`FULL OUTER` | 007, 009, 010 |
| `dictGet` onde o requisito é histórico (deveria ser `ASOF`) | 005 |
| desigualdade do `ASOF` fora da última posição do `ON` | 005 |
| média de razões | 003, 004, 005 |
| janela de recálculo menor que o atraso real | 001, 005 |
| janela onde não deveria haver janela | 008 |
