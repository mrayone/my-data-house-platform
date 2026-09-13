# my-data-house-platform

PoC de uma **plataforma de agregação sobre ClickHouse**, alimentada por N tópicos
Kafka (Confluent) em Avro, produzidos por CDC do SAP via Datasphere.

**Duas perguntas a responder:**

1. O ClickHouse consolida, deduplica e agrega ~30 tópicos CDC correlacionados usando
   **apenas mecanismos nativos** — materialized views, engines de merge,
   dictionaries — sem um motor de transformação externo?
2. Vale contratar o **ClickHouse Cloud no GCP**? A PoC roda self-hosted para produzir
   a medição que sustenta essa decisão.

---

## O desenho em uma frase

**Ingestão burra, modelagem rica.** O Kafka Connect Sink joga cada tópico numa
tabela de landing própria, sem transformar nada. Toda a inteligência — dedup de CDC,
resolução de delete, correlação entre entidades, agregação — acontece dentro do
ClickHouse, em quatro camadas.

```
SAP ──CDC──> Datasphere ──Avro──> Confluent ──> Kafka Connect (ClickHouse Sink)
                                                        │  exactly-once
╔═══════════════════════════════════════════════════════▼══════════════════════╗
║  L0  dh_landing   append-only, 1 tabela por tópico, fiel ao Avro             ║
║  L1  dh_core      estado atual — ReplacingMergeTree(_cdc_seq, _is_deleted)   ║
║  L2  dh_marts     correlações e agregados — Refreshable MV, Dictionary       ║
║  L3  dh_reports   contrato de leitura                                        ║
╚═════════════════════════════╤════════════════════════════════════════════════╝
                   ┌──────────┴──────────┐
                   ▼                     ▼
             API (aplicação)        BI / analista
```

Detalhes: [`docs/architecture/overview.md`](docs/architecture/overview.md).

---

## Stack

| Camada | Escolha |
|---|---|
| Armazenamento/compute | ClickHouse **self-hosted** (paridade com o Cloud) |
| Coordenação | ClickHouse Keeper (requisito do exactly-once) |
| Streaming | Kafka + Confluent Schema Registry (Avro) |
| Ingestão | **ClickHouse Kafka Connect Sink** |
| Linguagem | **Go** — `dhctl` (codegen, migrations), `producer` (carga CDC), `api` (serving) |
| Orquestração local | Docker Compose |

---

## Como rodar

> **Status:** Fase 00 (bootstrap) concluída. Fase 01 (ambiente local via Docker
> Compose) tem o `docker-compose.yml` e os alvos `up`/`down`/`ps`/`logs` prontos,
> mas ainda **não validada de ponta a ponta em máquina com Docker** nesta sessão
> de execução (bloqueio registrado em
> [`docs/plan/PROGRESS.md`](docs/plan/PROGRESS.md#bloqueios)). `bootstrap`
> (tópicos/schemas/connectors via `dhctl`) e `seed`/`reports` orientados a
> contrato chegam nas Fases 02-04. Enquanto isso, os alvos `db-apply` /
> `mock-load` / `mock-verify` abaixo já sobem o schema completo e provam o
> fluxo landing → core → marts com dados sintéticos — ver
> [`docs/evaluation/spike-mock-flow-clickhouse-kafka.md`](docs/evaluation/spike-mock-flow-clickhouse-kafka.md).

```bash
make tools       # instala as ferramentas de dev em ./bin (gofumpt; golangci-lint
                 # quando a rede do ambiente resolver — ver docs/TECH-DEBT.md)
make verify      # fmt-check + lint + test + checks de fronteira de contexto
make build       # compila dhctl, producer e api em ./bin
```

### Ambiente local (Docker Compose)

```bash
make up          # sobe ClickHouse, Keeper, Kafka, Schema Registry, Connect
                 # (docker-compose.yml) e espera todos os healthchecks
make ps          # lista os serviços e o status de saúde
make logs SERVICE=kafka-connect   # segue os logs de um serviço (ou de todos, sem SERVICE)
make down        # derruba o ambiente (mantém os volumes)
make reset-env   # derruba e remove os volumes (estado zerado)
```

Requisitos e diagnóstico em
[`docs/runbooks/local-environment.md`](docs/runbooks/local-environment.md)
(runbook ainda placeholder — ver nota do bloqueio acima).

### Schema + dados sintéticos (spike — enquanto `dhctl`/Kafka real não existem)

Com o ambiente de cima no ar (`make up`), estes três alvos aplicam todo o DDL
documentado nos ADRs e carregam um dataset determinístico que já exercita os
casos adversos dos cenários (fora de ordem, delete, ressurreição, órfão,
ASOF JOIN, retentativa de pagamento etc.):

```bash
make db-apply    # aplica bootstrap + landing + core + dictionaries + marts
                 # (internal/contexts/*/sql, db/shared/00-bootstrap)
make mock-load   # gera o dataset sintético (cmd/producer mock) e carrega
                 # cada entidade em dh_landing.<contexto>__<entidade>_raw
make mock-verify # roda a matriz de verificação do spike (14 checagens)
```

`producer mock` escreve NDJSON em `./out/mock/` e insere via
`clickhouse-client ... FORMAT JSONEachRow` — um stand-in explícito para o
producer Avro + Kafka Connect Sink reais da Fase 03, não o transporte final.
Metodologia completa, o que foi e o que não foi provado, e os bugs de DDL
encontrados no caminho:
[`docs/evaluation/spike-mock-flow-clickhouse-kafka.md`](docs/evaluation/spike-mock-flow-clickhouse-kafka.md).

`make help` lista todos os alvos disponíveis a qualquer momento.

---

## Mapa da documentação

| Você quer... | Vá para |
|---|---|
| entender o sistema | [`docs/architecture/overview.md`](docs/architecture/overview.md) |
| saber por que cada decisão foi tomada | [`docs/adr/README.md`](docs/adr/README.md) |
| saber o que a PoC tem de provar | [`docs/scenarios/README.md`](docs/scenarios/README.md) |
| **implementar** | [`docs/plan/implementation-plan.md`](docs/plan/implementation-plan.md) |
| saber o que já foi feito | [`docs/plan/PROGRESS.md`](docs/plan/PROGRESS.md) |
| operar o sistema | [`docs/runbooks/README.md`](docs/runbooks/README.md) |
| decidir sobre o ClickHouse Cloud | [`docs/evaluation/README.md`](docs/evaluation/README.md) |
| plugar um tópico novo | [`docs/runbooks/add-new-topic.md`](docs/runbooks/add-new-topic.md) |
| trabalhar neste repositório | [`CLAUDE.md`](CLAUDE.md) — **leia antes de alterar qualquer coisa** |

---

## As quatro decisões que definem o sistema

| Decisão | Por quê | ADR |
|---|---|---|
| Ingestão pelo **ClickHouse Kafka Connect Sink**, não por consumer próprio nem Kafka Engine | é o análogo self-hosted do ClickPipes; exactly-once pronto; valida o produto que vamos contratar, não o nosso código | [0002](docs/adr/0002-ingestao-via-clickhouse-kafka-connect-sink.md) |
| **Um contrato YAML por entidade** gera schema Avro, DDL e config de connector | 30 tópicos × 6 artefatos à mão é inconsistência garantida; aqui divergência é erro de CI | [0003](docs/adr/0003-extensibilidade-contract-first.md) |
| **CDC resolvido na leitura**, com `ReplacingMergeTree(_cdc_seq, _is_deleted)` e views `v_*_current` | corretude não pode depender de o merge ter rodado nem da ordem de chegada | [0004](docs/adr/0004-semantica-cdc-dedup-ordem-delete.md) |
| **Árvore de decisão fixa** entre MV incremental, Refreshable MV, Dictionary e JOIN em query | MV incremental **não é join de streams**; usá-la para correlação grava número errado e nunca corrige | [0005](docs/adr/0005-estrategia-de-agregacao-mv-refreshable-dictionary.md) |

---

## Extensibilidade: o requisito central

Plugar um tópico novo custa **2 arquivos escritos à mão, 0 linhas de Go, 0 arquivos
existentes alterados**:

```bash
$EDITOR contracts/domains/<contexto>/<entidade>.yaml   # 1
make generate                                          # gera avsc, DDL, connector, tópico
$EDITOR internal/contexts/<ctx>/sql/20-core/...sql      # 2 (a partir do esqueleto gerado)
make bootstrap
```

Todo arquivo gerado carrega `GENERATED by dhctl` e um `contract-hash`; `make verify`
falha se algum divergir do contrato. **Inconsistência entre schema, DDL e connector
deixa de ser possível: é erro de build.**

Essa promessa é **medida**, não afirmada:
[`docs/evaluation/extensibility-drill.md`](docs/evaluation/extensibility-drill.md).

Procedimento completo: [`docs/runbooks/add-new-topic.md`](docs/runbooks/add-new-topic.md).

---

## Os dez cenários de validação

A PoC não é válida por "subiu e ingeriu". É válida quando dez relatórios produzem
números que **fecham com o cálculo independente**, sob CDC fora de ordem, com delete
e com dado que chega atrasado.

Juntos eles exercitam todas as formas de correlação do cenário real — inclusive a
**ausência** de correlação:

| Forma de correlação | Cenário |
|---|---|
| por ID entre entidades mutáveis | [001](docs/scenarios/001-order-360.md), [003](docs/scenarios/003-payment-funnel.md), [008](docs/scenarios/008-customer-metrics.md) |
| por coluna que não é ID surrogate | [002](docs/scenarios/002-revenue-by-bu-day.md) |
| por código string sem integridade referencial | [004](docs/scenarios/004-discount-effectiveness.md) |
| temporal / por vigência (`ASOF JOIN`) | [005](docs/scenarios/005-real-margin-vs-price-list.md) |
| chave composta | [006](docs/scenarios/006-stock-coverage-vs-demand.md) |
| anti-join — o que **não** se conecta | [007](docs/scenarios/007-stockout-anti-join.md), [009](docs/scenarios/009-data-quality-orphans.md), [010](docs/scenarios/010-catalog-without-sales.md) |
| `FULL OUTER` entre entidades sem hierarquia | [010](docs/scenarios/010-catalog-without-sales.md) |

Índice e cobertura completa: [`docs/scenarios/README.md`](docs/scenarios/README.md).

---

## Contextos

Contexto é o eixo de organização em **todas** as dimensões do repositório:
contratos, schemas Avro, SQL e código Go. Contexto novo é uma pasta, sem nada a
registrar em lugar central e sem conflito de merge
([ADR-0009](docs/adr/0009-layout-de-pastas-context-first.md)).

| Contexto | Entidades |
|---|---|
| `sales` | `order`, `order_item`, `order_payment` |
| `customer` | `customer` |
| `organization` | `business_unit` |
| `pricing` | `prices`, `discount_codes` |
| `inventory` | `stock_position` |
| `analytics` | — (marts que cruzam contextos) |

---

## Estado do projeto

| Etapa | Status |
|---|---|
| Fundação: ADRs, arquitetura, cenários, contratos, plano | **concluída** |
| Fases 00-08: implementação | **não iniciada** |
| Fase 09: validação no ClickHouse Cloud | opcional |

Detalhe por fase e por tarefa: [`docs/plan/PROGRESS.md`](docs/plan/PROGRESS.md).
