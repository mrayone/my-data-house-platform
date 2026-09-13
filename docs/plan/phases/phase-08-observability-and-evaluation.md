# Fase 08 — Qualidade, benchmark e relatório de avaliação

- **Branch:** `feat/phase-08-observability-and-evaluation`
- **Pré-requisito:** Fase 07 `DONE`
- **Bloqueia:** — (Fase 09 é opcional)
- **ADRs relevantes:** [0010](../../adr/0010-observabilidade-e-qualidade-de-dados.md), [0008](../../adr/0008-topologia-self-hosted-e-paridade-com-clickhouse-cloud.md)
- **Entrega verificável:** `dhctl dq run --severity error` verde e `docs/evaluation/results.md` preenchido com números medidos

## Objetivo da fase

Transformar "funcionou" em afirmação verificável, e produzir o documento que
sustenta a decisão sobre o ClickHouse Cloud no GCP.

Esta fase não constrói funcionalidade nova: ela **mede**, e a medição é o
entregável. Um relatório com número estimado em vez de medido não serve para
decidir uma contratação.

---

## P08-T01 — Catálogo de checks e `dhctl dq run`

**Objetivo:** o catálogo do ADR-0010 implementado e executável.

**Arquivos:**
- `internal/platform/observability/dq.go`
- `internal/platform/observability/dlq.go`
- `cmd/dhctl/dq.go`
- `db/shared/00-bootstrap/0031__dq_catalog.sql` (seed do catálogo)
- `Makefile` (`dq`)

**Especificação:**
- `dh_meta.dq_checks(check_id, context, entity, severity, query, threshold, comparator, description)`
  populado por migration. `comparator` ∈ `lte|gte|eq|neq`.
- `dhctl dq run [--severity=error|warn|info] [--check=<id>] [--context=<ctx>]`:
  executa cada query, compara com o threshold pelo comparador, grava em
  `dh_meta.dq_check_results(check_id, run_at, value, passed, details)`, imprime
  tabela, e **sai 1 se algum check da severidade pedida falhar**.
- Checks que **não** são SQL puro (DLQ, `system.view_refreshes`,
  `system.dictionaries`) são implementados em Go em `observability/` e **gravam na
  mesma tabela de resultados** — o histórico tem de ser uniforme.
- **`dq.landing.dlq_depth`** consulta o Kafka/Connect (`dlq.*` por connector).
  Tentar medir isso em SQL produziria um check que nunca falha.
- Catálogo mínimo obrigatório: os 13 do ADR-0010 **mais** os introduzidos pelos
  cenários (`dq.marts.rate_bounds`, `dq.marts.header_item_parity`,
  `dq.money.no_negative_amount`, `dq.discount.over_limit`,
  `dq.discount.outside_validity`, `dq.price.coverage`, `dq.price.staleness`,
  `dq.price.below_cost`, `dq.marts.row_parity`, `dq.customer.cohort_bounds`,
  `dq.customer.signup_after_first_order`, `dq.marts.refresh_duration`,
  `dq.catalog.coverage`, `dq.catalog.stock_without_price`,
  `dq.catalog.class_exhaustive`, `dq.stock.negative_available`).
- `dhctl dq list` imprime o catálogo com severidade e threshold.
- `make verify` passa a incluir `dhctl dq run --severity=error`.

**Critérios de aceite:**
- [ ] `dhctl dq list` mostra todos os checks do ADR-0010 e dos 10 cenários
- [ ] `dhctl dq run --severity error` sai 0 com o pipeline saudável
- [ ] sai **1** com um connector pausado (freshness + DLQ)
- [ ] sai **1** se uma MV de core for derrubada (reconciliação)
- [ ] `dq.marts.sum_parity` falha se um mart for corrompido de propósito com um
      `INSERT` manual (teste e reverter com `mart refresh`)
- [ ] todo resultado fica em `dh_meta.dq_check_results` com `value` e `passed`
- [ ] `make verify` inclui o `dq run`

**Validação:** `make dq && clickhouse-client -q "SELECT check_id, value, passed FROM dh_meta.dq_check_results WHERE run_at > now() - INTERVAL 10 MINUTE ORDER BY passed, check_id"`

**Commit:** `feat(platform): adicionar catálogo de checks de qualidade e dhctl dq run`

**Docs a atualizar:** `PROGRESS.md`

---

## P08-T02 — Métricas de recurso e observabilidade

**Objetivo:** coletar o que alimenta a cotação do Cloud.

**Arquivos:**
- `internal/platform/observability/metrics.go`
- `cmd/dhctl/metrics.go`
- `deploy/observability/docker-compose.observability.yml` (opcional: Prometheus + Grafana)
- `deploy/observability/dashboards/*.json` (opcional)
- `docs/runbooks/observability.md`

**Especificação:**
- `dhctl metrics snapshot` coleta e imprime (e grava em
  `dh_meta.pipeline_health`):

  | Fonte | O que |
  |---|---|
  | `system.parts` | linhas, `bytes_on_disk`, `data_uncompressed_bytes`, parts ativos por tabela e por database |
  | `system.metrics` / `asynchronous_metrics` | memória, conexões, merges em andamento |
  | `system.events` | `SelectedRows`, `InsertedRows`, `MergedRows`, `FailedQuery` |
  | `system.query_log` | p50/p95/p99 de duração por usuário e por tabela |
  | `system.view_refreshes` | duração e resultado do último refresh por MV |
  | `system.dictionaries` | `element_count`, `bytes_allocated`, `status` |
  | Kafka | lag por consumer group, profundidade de DLQ |

- **Razão de compressão por tabela** (`data_uncompressed_bytes / bytes_on_disk`) é
  obrigatória: é o número que converte volume de negócio em custo de storage.
- Stack de observabilidade é **opcional** e fica em compose separado — não pesa o
  ambiente base. Se implementada, dashboards mínimos: ingestão (lag, taxa, DLQ),
  saúde de marts (refresh), recursos (CPU, memória, disco), latência de API.
- `docs/runbooks/observability.md`: o que olhar quando algo está lento, com as
  queries prontas.

**Critérios de aceite:**
- [ ] `dhctl metrics snapshot` imprime as 7 seções
- [ ] razão de compressão por tabela presente
- [ ] p95 por usuário (`dh_app` vs `dh_analyst`) separado
- [ ] lag de consumer group por tópico
- [ ] o compose de observabilidade sobe sem afetar o base (se implementado)
- [ ] o runbook tem query pronta para cada sintoma listado

**Validação:** `./bin/dhctl metrics snapshot`

**Commit:** `feat(platform): adicionar coleta de métricas de recurso`

**Docs a atualizar:** `PROGRESS.md`; `docs/runbooks/observability.md`

---

## P08-T03 — Benchmark

**Objetivo:** números reproduzíveis, com o dataset fixado no plano.

**Arquivos:**
- `test/bench/ingestion_bench.go`
- `test/bench/query_bench.go`
- `test/bench/refresh_bench.go`
- `scripts/bench.sh`
- `Makefile` (`bench`)
- `docs/runbooks/benchmark.md`

**Especificação:**

`make bench` executa, contra o dataset `--scale=bench` (~137 M mensagens, definido
em [`../implementation-plan.md`](../implementation-plan.md)):

1. **Ingestão:** mensagens/s por tópico e agregado; lag máximo; CPU e memória do
   ClickHouse e do Connect durante. **Rodar duas vezes: com
   `exactlyOnce=true` e com `false`**, e comparar — é o insumo do risco de
   throughput registrado no plano.
2. **L0→L1:** tempo até a linha aparecer em `v_*_current` (p50/p95/p99).
3. **Refresh dos marts:** duração e memória de cada Refreshable MV, com destaque
   para `customer_metrics` (refresh total) e `idle_catalog_items` (catálogo inteiro).
4. **Queries aplicacionais:** p50/p95/p99 de `/orders/{id}/360` e
   `/customers/{id}/metrics`, sob 1, 10 e 50 conexões concorrentes.
5. **Queries analíticas:** cada uma das 10 queries de consumo dos cenários,
   duração e linhas lidas.
6. **`FINAL` vs `argMax`:** a mesma leitura de L1 pelas duas formas, para
   quantificar o custo da view corrente (ADR-0004 §4).
7. **Storage:** bytes por camada, razão de compressão, parts ativos.
8. **Interferência entre perfis:** rodar a carga analítica e a aplicacional
   **simultaneamente** e medir a degradação do p95 aplicacional. É o risco
   declarado no ADR-0011 (isolamento imperfeito em single-node).

Regras:
- Toda medição registra: versão do ClickHouse, versão do plugin, semente do
  dataset, recursos do host (CPU, RAM, tipo de disco), e data.
- Saída em JSON **e** em markdown, para colar em `results.md`.
- `docs/runbooks/benchmark.md`: como reproduzir do zero, quanto tempo leva, quanto
  disco precisa.

**Critérios de aceite:**
- [ ] `make bench` completa e produz JSON + markdown
- [ ] os 8 blocos têm número
- [ ] a comparação `exactlyOnce` true vs false está presente
- [ ] a comparação `FINAL` vs `argMax` está presente
- [ ] o teste de interferência entre perfis está presente
- [ ] toda medição tem versão, semente e recursos do host registrados
- [ ] rodar de novo com a mesma semente dá resultado na mesma ordem de grandeza

**Validação:** `make bench`

**Commit:** `test(platform): adicionar suíte de benchmark`

**Docs a atualizar:** `PROGRESS.md`; `docs/runbooks/benchmark.md`

---

## P08-T04 — Relatório de avaliação do ClickHouse Cloud no GCP

**Objetivo:** o documento de decisão. **É o entregável final da PoC.**

**Arquivos:**
- `docs/evaluation/results.md`
- `docs/evaluation/clickhouse-cloud-gcp-criteria.md` (completar)
- `docs/evaluation/README.md`

**Especificação:**

`clickhouse-cloud-gcp-criteria.md` (iniciado na Fase 01) fica completo com:

1. **Pré-requisitos bloqueantes**, com a coluna `CLOUD` **resolvida** e evidência
   citada (link de doc oficial, ou teste na Fase 09):
   Refreshable MV · KeeperMap · `ASOF JOIN` · Dictionary `COMPLEX_KEY_HASHED` ·
   `ReplacingMergeTree(version, is_deleted)` · Projection · `ON CLUSTER`.
2. **O que muda na migração:** a tabela "peça self-hosted → equivalente no Cloud",
   com a ingestão (Connect → ClickPipes) como **única** substituição, e por que
   L0-L3 migram sem reescrita.
3. **O que a PoC NÃO prova** (ADR-0008): custo real, autoscaling, SLA, DR. Escrito
   de forma destacada, para que a decisão não se apoie em medição inexistente.
4. **Insumos da cotação:** volume por camada, razão de compressão, taxa de ingestão
   sustentada, CPU/RAM no pico, IOPS, e a projeção para o volume real (declarando o
   fator de extrapolação usado e que extrapolação não é medição).
5. **Comparativo de alternativas**, como colunas e não como recomendação:
   ClickHouse Cloud no GCP · ClickHouse self-hosted em GKE/GCE · ClickHouse
   gerenciado por terceiro · manter o estado atual. Critérios: esforço de operação,
   risco técnico, o que já está provado pela PoC, e o que continuaria em aberto.
6. **Riscos residuais** e o que faria cada um mudar a decisão.

`results.md` consolida **todos** os números medidos: os 8 blocos do benchmark, as
durações de refresh de cada mart, `bytes_allocated` dos dictionaries, p95 da API,
storage por camada, e o resultado do exercício de extensibilidade da Fase 07.

`docs/evaluation/README.md`: índice e, em cinco linhas, **a leitura dos números** —
o que eles sustentam e o que não sustentam.

**Regras de escrita:**
- Todo número tem fonte (comando ou query) e data.
- Nenhuma recomendação de compra. O documento apresenta evidência; a decisão é de
  quem tem o contexto comercial.
- Onde não houver medição, escreva **"não medido"** — nunca uma estimativa
  apresentada como número.

**Critérios de aceite:**
- [ ] a tabela de pré-requisitos tem a coluna `CLOUD` resolvida com evidência
      citada, ou marcada `A VERIFICAR NA FASE 09` de forma explícita
- [ ] `results.md` tem os 8 blocos do benchmark com números reais
- [ ] a seção "O que a PoC não prova" está presente e destacada
- [ ] o comparativo tem as 4 alternativas com os mesmos critérios
- [ ] todo número tem fonte e data
- [ ] nenhum número estimado aparece sem estar rotulado como extrapolação
- [ ] o documento é legível por alguém que não acompanhou a implementação

**Validação:** revisão humana — esta tarefa **não** se autoaprova

**Commit:** `docs(plan): consolidar relatório de avaliação do ClickHouse Cloud`

**Docs a atualizar:** `PROGRESS.md`; `README.md` (link para a avaliação)

---

## P08-T05 — Fechamento: revisão de docs e dívidas

**Objetivo:** o repositório conta a verdade sobre si mesmo.

**Arquivos:** `README.md`, `docs/plan/PROGRESS.md`, `docs/adr/*` (se necessário), `docs/TECH-DEBT.md`

**Especificação:**
- **Revisar todo ADR** contra o que foi realmente implementado. Divergência ⇒ ADR
  novo com `Supersedes:`, **nunca** editar o aceito.
- **Revisar os 10 docs de cenário** contra o SQL implementado: coluna, regra e
  critério de aceite têm de bater.
- `README.md` final: o que é o projeto, como subir em 3 comandos, o mapa da
  documentação, e o link para a avaliação.
- `docs/TECH-DEBT.md`: o que ficou de fora, o que ficou frágil, o que um próximo
  passo deveria atacar — com a fase/tarefa de origem de cada item. Inclui,
  obrigatoriamente, os achados do exercício de extensibilidade (Fase 07 T07) que
  não foram corrigidos.
- `PROGRESS.md` com todas as fases `DONE` e um resumo executivo de 10 linhas.

**Critérios de aceite:**
- [ ] nenhum ADR aceito contradiz a implementação (ou existe ADR novo)
- [ ] nenhum doc de cenário contradiz o SQL implementado
- [ ] `README.md` permite subir o projeto sem ler mais nada
- [ ] `docs/TECH-DEBT.md` existe e cada item tem origem rastreável
- [ ] `PROGRESS.md` completo
- [ ] todos os links internos da documentação resolvem (rode um verificador de links)

**Validação:**
```bash
make verify && make dq
# verificador de links markdown sobre docs/ e os CLAUDE.md
```

**Commit:** `docs: fechar documentação da PoC e registrar dívidas técnicas`

**Docs a atualizar:** todos os citados

---

## Critérios de aceite da fase

- [ ] `dhctl dq run --severity error` verde
- [ ] catálogo de checks completo (ADR-0010 + os dos 10 cenários)
- [ ] `dhctl metrics snapshot` com as 7 seções
- [ ] `make bench` com os 8 blocos medidos
- [ ] `docs/evaluation/results.md` preenchido com números reais e fontes
- [ ] `docs/evaluation/clickhouse-cloud-gcp-criteria.md` com os pré-requisitos resolvidos
- [ ] `docs/TECH-DEBT.md` escrito
- [ ] nenhum ADR contradizendo a implementação
- [ ] todos os links internos resolvem
- [ ] `PROGRESS.md` com a Fase 08 `DONE` e resumo executivo
