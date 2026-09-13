# Fase 07 — Marts, parte 2 (cenários 006-010), API e extensibilidade medida

- **Branch:** `feat/phase-07-marts-part-2-and-api`
- **Pré-requisito:** Fase 06 `DONE`
- **Bloqueia:** Fase 08
- **ADRs relevantes:** [0005](../../adr/0005-estrategia-de-agregacao-mv-refreshable-dictionary.md), [0011](../../adr/0011-serving-aplicacional-e-analitico-no-mesmo-store.md), [0003](../../adr/0003-extensibilidade-contract-first.md)
- **Entrega verificável:** 10 marts, API com p95 medido, e a nona entidade plugada em < 30 min sem Go novo

## Objetivo da fase

Fechar os dez cenários, provar o requisito aplicacional com uma API real, e —
o mais importante para a tese do projeto — **medir o custo de plugar a nona
entidade**. Se esse custo não for 2 arquivos e 0 linhas de Go, a plataforma não é
extensível, e isso é um achado da PoC.

> **Paralelizável:** T01 a T05 (os cinco cenários) são independentes. T06 (API)
> depende de T03 (cenário 008). T07 depende de tudo.

---

## P07-T01 — Cenário 006: `stock_coverage_dc_item`

**Objetivo:** implementar [`../../scenarios/006-stock-coverage-vs-demand.md`](../../scenarios/006-stock-coverage-vs-demand.md).

**Especificação:** o doc do cenário. Pontos de atenção:
- Correlação por **chave composta** (`item_id`, `dc_id`).
- `order_item.dc_id` é `Nullable`: o doc define como tratar item não alocado — siga
  exatamente, e não invente coalesce silencioso.
- Janela de demanda de 28 dias, com `demand_basis_from`/`to` gravados na linha
  (rastreabilidade do denominador).
- Entidade de alta taxa de atualização: confirme que a leitura é por
  `v_inventory__stock_position_current`.

**Critérios de aceite:** os do doc, com destaque para:
- [ ] cobertura em dias correta para um par com demanda conhecida
- [ ] par abaixo do `safety_stock` sinalizado
- [ ] item com `dc_id` nulo tratado conforme a regra do doc (e não descartado em
      silêncio)
- [ ] paridade da demanda com o cálculo independente = 0

**Validação:** `go test ./test/e2e/ -run TestStockCoverage -v`

**Commit:** `feat(analytics): implementar cenário 006 - cobertura de estoque vs demanda`

**Docs a atualizar:** `PROGRESS.md`; `docs/evaluation/results.md`

---

## P07-T02 — Cenário 007: `stockout_candidates`

**Objetivo:** implementar [`../../scenarios/007-stockout-anti-join.md`](../../scenarios/007-stockout-anti-join.md).

**Especificação:** o doc do cenário. Pontos de atenção:
- **Anti-join com `LEFT JOIN` + teste de coluna vazia (`= ''`), nunca `IS NULL`.**
  Coluna não-`Nullable` do lado ausente vem como valor padrão do tipo. Usar
  `IS NULL` retorna **zero** candidatos, silenciosamente — é a armadilha mais
  perigosa do cenário.
- As **três classes** do doc são distintas e mutuamente exclusivas.
- Janela temporal obrigatória: sem ela, dado atrasado é confundido com dado ausente.

**Critérios de aceite:** os do doc, com destaque para:
- [ ] as três classes produzem resultado, cada uma com o caso nomeado do gerador
- [ ] o anti-join **de fato conta** (se o resultado for sempre zero, o `IS NULL` foi
      usado — revisar antes de seguir)
- [ ] item vendido em `dc_id` sem posição para aquele item é detectado
- [ ] a janela impede falso positivo de dado recém-atrasado

**Validação:** `go test ./test/e2e/ -run TestStockout -v`

**Commit:** `feat(analytics): implementar cenário 007 - ruptura por anti-join`

**Docs a atualizar:** `PROGRESS.md`; `docs/evaluation/results.md`

---

## P07-T03 — Cenário 008: `customer_metrics` e `customer_cohort_month`

**Objetivo:** implementar [`../../scenarios/008-customer-metrics.md`](../../scenarios/008-customer-metrics.md).

**Especificação:** o doc do cenário. Pontos de atenção:
- **Sem janela de recálculo** — substituição total. A seção "Por que não há janela"
  do doc explica; não "otimize" introduzindo janela.
- `FROM` é o **cliente**, `LEFT JOIN` com os pedidos: cliente sem pedido tem de
  aparecer, senão a retenção da coorte fica inflada.
- `recency_days` e `avg_days_between_orders` `Nullable`, nunca `0`.
- Uma `PROJECTION` (`p_by_cohort_segment`), justificada no doc.
- **Meça a duração do refresh total** e registre — é o número que diz se a
  estratégia sem janela escala.

**Critérios de aceite:** os do doc, com destaque para:
- [ ] **cancelamento de pedido de 200 dias atrás corrige o `lifetime_net`** (é o
      teste que valida a ausência de janela)
- [ ] `CUST-SEM-PEDIDO` aparece com `never_purchased = 1` e `recency_days = NULL`
- [ ] `retention_rate` sempre em `[0,1]`, `cohort_size` estável entre meses
- [ ] a projection é usada (`EXPLAIN indexes = 1`)
- [ ] duração do refresh total em `results.md`, com o número de clientes

**Validação:** `go test ./test/e2e/ -run TestCustomerMetrics -v`

**Commit:** `feat(analytics): implementar cenário 008 - métricas e coorte de cliente`

**Docs a atualizar:** `PROGRESS.md`; `docs/evaluation/results.md`

---

## P07-T04 — Cenário 009: painel de qualidade **gerado**

**Objetivo:** implementar [`../../scenarios/009-data-quality-orphans.md`](../../scenarios/009-data-quality-orphans.md).

**Arquivos:** além dos do doc, `internal/codegen/ddl/dq_overview.go` e os templates.

**Especificação:** o doc do cenário. **Esta tarefa é diferente das outras: o SQL é
gerado a partir dos contratos**, não escrito à mão — são 8 entidades e ~12 relações,
e escrever isso à mão garantiria divergência (ADR-0003).

Pontos de atenção:
- O gerador produz um bloco `SELECT ... UNION ALL` por entidade (painel) e por
  relação declarada (orfandade).
- **`kind` da relação decide o check:** `loose` **não** gera `dq.rel.orphan_rate`.
  O gerador lê isso do contrato.
- `orphan_threshold_pct` de cada relação vem do contrato;
  `freshness_sla_seconds` vem de `source.freshness_sla_minutes`.
- `refresh_window_s` de cada relação é a janela do mart que a consome — mapeamento
  declarado em `internal/codegen/ddl/dq_windows.go` com comentário apontando o
  cenário.
- As duas exceções que leem a tabela core direto levam o marcador
  `-- dq-exception: reads core table intentionally`, e `no-direct-core-read.sh` as
  ignora.
- A idade do órfão é medida por `_ingested_at`, não `_kafka_ts`.

**Critérios de aceite:** os do doc, com destaque para:
- [ ] o painel tem **exatamente** uma linha por contrato (é gerado, logo é completo)
- [ ] o resumo de orfandade tem uma linha por entrada `relations` dos contratos
- [ ] **órfão transitório aparece e desaparece sozinho** após o refresh seguinte
- [ ] órfão com 4 dias (janela 3) dispara `age_ok = 0`
- [ ] reconciliação detecta MV derrubada temporariamente
- [ ] freshness respeita o SLA por entidade (`order` 10 min vs `business_unit` 24 h)
- [ ] relação `loose` (`prices ↔ stock_position`) **não** gera `orphan_rate`
- [ ] `dh_reports.v_dq_alerts` vazia em operação normal

**Validação:** `make generate && make migrate && go test ./test/e2e/ -run TestDataQuality -v`

**Commit:** `feat(analytics): implementar cenário 009 - painel de qualidade gerado dos contratos`

**Docs a atualizar:** `PROGRESS.md`

---

## P07-T05 — Cenário 010: `idle_catalog_items`

**Objetivo:** implementar [`../../scenarios/010-catalog-without-sales.md`](../../scenarios/010-catalog-without-sales.md).

**Especificação:** o doc do cenário. Pontos de atenção:
- **`FULL OUTER JOIN`** entre preço e estoque: nenhum dos dois é pai do outro
  (relação `loose`), então nenhum pode ser a base exclusiva.
- Duas detecções de ausência no mesmo SQL — `= ''` em ambas, nunca `IS NULL`.
- `has_price` e `has_stock_record` como colunas explícitas: uma vez calculadas
  certo, ninguém mais precisa acertar a comparação.
- `idle_class` mutuamente exclusiva; `indefinido` deve ficar em zero.
- Preço vigente por `argMax(..., valid_from)` — aqui a referência é **agora**, não a
  data do pedido (diferente do cenário 005).
- `available` negativo **não** é zerado.
- É o refresh mais caro da PoC: registre a duração.

**Critérios de aceite:** os do doc, com destaque para:
- [ ] as **seis** classes produzem resultado com os SKUs nomeados do gerador
- [ ] `SKU-PRECO-SEM-POSICAO` aparece (prova que o `FULL OUTER` funciona)
- [ ] `SKU-SEM-PRECO-COM-ESTOQUE` aparece como `estoque_sem_preco`
- [ ] venda cancelada **não** reativa o item
- [ ] venda de 120 dias atrás não conta como atividade, mas
      `days_since_last_sale = 120`
- [ ] `idle_class = 'indefinido'` em zero linhas
- [ ] duração do refresh e tamanho do catálogo em `results.md`

**Validação:** `go test ./test/e2e/ -run TestIdleCatalog -v`

**Commit:** `feat(analytics): implementar cenário 010 - catálogo parado`

**Docs a atualizar:** `PROGRESS.md`; `docs/evaluation/results.md`

---

## P07-T06 — API de serving

**Objetivo:** provar o requisito "servir aplicações", com latência medida.

**Arquivos:**
- `internal/reporting/http/server.go`
- `internal/reporting/http/order_handler.go`
- `internal/reporting/http/customer_handler.go`
- `internal/reporting/http/health_handler.go`
- `internal/reporting/query/registry.go`
- `cmd/api/main.go`
- `Makefile` (`api`, `api-smoke`)
- `test/e2e/api_test.go`
- `docs/runbooks/api.md`

**Especificação (ADR-0011 §5):**
- Endpoints, e **somente** estes:

  | Método | Rota | Fonte | Frescor |
  |---|---|---|---|
  | GET | `/orders/{order_id}/360` | `dh_marts.order_360` | ≤ 90 s |
  | GET | `/customers/{customer_id}/metrics` | `dh_marts.v_customer_metrics` | ≤ 30 min |
  | GET | `/health` | — | liveness do processo |
  | GET | `/health/data` | `dh_reports.v_dq_alerts` | `200` se vazia, `503` se houver alerta `error` |

- **Não existe endpoint que aceite SQL do cliente.** Cada endpoint mapeia para uma
  query nomeada e versionada em `internal/contexts/*/reports/`, registrada em
  `internal/reporting/query/registry.go`.
- Conecta como role **`dh_app`** (`max_execution_time=3`, `readonly=1`). Query lenta
  **falha rápido** e vira `504` com log — é o desenho, não um defeito.
- Toda resposta traz o header `X-Data-Freshness` com a idade do dado
  (`_refreshed_at` da linha) e `X-Data-Source` com a tabela.
- `404` para chave inexistente; `400` para chave malformada; `503` quando
  `/health/data` está vermelho e o endpoint depende de mart degradado.
- Log estruturado por request: rota, chave, duração, linhas, status. **Sem PII e
  sem senha.**
- Timeout de contexto propagado para a query do ClickHouse.
- `make api-smoke`: sobe a API, bate nos 4 endpoints com dados conhecidos, e falha
  se algum não responder como esperado.

**Critérios de aceite:**
- [ ] os 4 endpoints respondem
- [ ] `GET /orders/ORD-ORPHAN/360` retorna `404` ou linha incompleta conforme o doc
      do cenário 001 (documente qual, e seja consistente)
- [ ] `X-Data-Freshness` presente e plausível em toda resposta de dado
- [ ] **p95 de `/orders/{id}/360` < 50 ms** com o dataset `dev` (medido, registrado)
- [ ] **p95 de `/customers/{id}/metrics` < 50 ms**
- [ ] query proposital sobre 90 dias pelo role `dh_app` retorna `504` (prova o
      `max_execution_time`)
- [ ] `/health/data` retorna `503` com um connector pausado
- [ ] nenhuma senha nem PII nos logs (`LOG_LEVEL=debug`)
- [ ] `make api-smoke` verde

**Validação:** `make api-smoke && go test ./test/e2e/ -run TestAPI -v`

**Commit:** `feat(reporting): adicionar API de serving com contrato de consulta nomeado`

**Docs a atualizar:** `PROGRESS.md`; `docs/runbooks/api.md`; `docs/evaluation/results.md` (p95)

---

## P07-T07 — **Prova de extensibilidade**: plugar a nona entidade

**Objetivo:** medir o custo real de plugar um tópico novo. **Este é o experimento
central da tese do projeto.**

**Arquivos:**
- `contracts/domains/inventory/stock_movement.yaml` (escrito à mão)
- `internal/contexts/inventory/sql/20-core/0030__stock_movement.sql` (a partir do esqueleto)
- `scripts/ext-drill.sh`
- `docs/evaluation/extensibility-drill.md`

**Especificação:**

Execute como um **exercício cronometrado**, e registre o que aconteceu de verdade —
inclusive o que deu errado.

1. Marque o tempo inicial.
2. Escreva `contracts/domains/inventory/stock_movement.yaml`: entidade de
   movimentação de estoque (campos `movement_id`, `item_id`, `dc_id`,
   `movement_type`, `quantity`, `unit_cost`, `moved_at`, `created_at`,
   `updated_at`), com relações `lookup` para `inventory/stock_position` e
   `pricing/prices`. O exemplo completo está em
   [`../../architecture/extensibility.md`](../../architecture/extensibility.md).
3. `dhctl contract validate`
4. `make generate`
5. Renomeie o esqueleto `20-core/0030__stock_movement.sql.tmpl` para `.sql` e
   complete-o.
6. `make bootstrap`
7. Gere carga: **use o gerador genérico**, sem escrever gerador específico. Se isso
   não for possível, **é um achado** — registre.
8. `dhctl dq run --severity error`
9. Marque o tempo final.

`scripts/ext-drill.sh` automatiza os passos 3-8 e **conta**:
arquivos escritos à mão, arquivos existentes alterados, linhas de Go escritas,
número de comandos, e o tempo total.

`docs/evaluation/extensibility-drill.md` registra:

| Item | Meta | Medido | Observação |
|---|---|---|---|
| Arquivos escritos à mão | 2 | | |
| Arquivos existentes alterados | 0 | | |
| Linhas de Go escritas | 0 | | |
| Comandos até dado agregado | 3 | | |
| Tempo de ponta a ponta | < 30 min | | |

Mais uma seção **"O que atrapalhou"**, honesta: cada ponto em que o gerador ficou
devendo, cada decisão que o contrato não expressava, cada erro de mensagem confusa.

**Critérios de aceite:**
- [ ] a nona entidade está ingerindo e com camada core funcionando
- [ ] `git diff --stat` mostra **zero** arquivos Go alterados
- [ ] `git diff --stat` mostra **zero** arquivos pré-existentes alterados (salvo
      docs e `PROGRESS.md`)
- [ ] `dhctl dq run --severity error` verde para a nova entidade
- [ ] `docs/evaluation/extensibility-drill.md` preenchido com os números **reais**
- [ ] a seção "O que atrapalhou" existe e não está vazia **se** algo atrapalhou —
      registrar problema é o objetivo, não falhar o exercício
- [ ] **se alguma meta estourou:** a causa está registrada e há uma tarefa de
      correção do gerador criada (pode ficar para a Fase 08)

**Validação:** `scripts/ext-drill.sh && git diff --stat`

**Commit:** `feat(inventory): plugar stock_movement como prova de extensibilidade`

**Docs a atualizar:** `PROGRESS.md`; `docs/evaluation/extensibility-drill.md`; `contracts/CLAUDE.md` (tabela de contratos)

---

## Critérios de aceite da fase

- [ ] 10 marts implementados, um por cenário
- [ ] paridade = 0 em todos os dez
- [ ] anti-joins **de fato contam** (nenhum retornando zero por uso de `IS NULL`)
- [ ] relação `loose` não gera check de orfandade
- [ ] API com os 4 endpoints e **p95 < 50 ms** nos dois de dado
- [ ] `/health/data` reflete o estado real do pipeline
- [ ] **nona entidade plugada com 2 arquivos à mão e 0 linhas de Go**
- [ ] `docs/evaluation/extensibility-drill.md` preenchido com números reais
- [ ] `make verify` passa
- [ ] `PROGRESS.md` com a Fase 07 `DONE`
