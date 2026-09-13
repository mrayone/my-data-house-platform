# Critérios de avaliação — ClickHouse Cloud no GCP

**Status:** esqueleto. Seções marcadas `A PREENCHER` são preenchidas nas fases
indicadas.

Referência: [ADR-0008](../adr/0008-topologia-self-hosted-e-paridade-com-clickhouse-cloud.md).

---

## 1. A pergunta

A PoC roda **self-hosted** porque o objetivo é decidir se vale contratar —
contratar antes de decidir inverteria a ordem. A consequência é que a PoC precisa
ser **isomórfica ao destino**: usar somente mecanismos que existam nos dois lados, e
isolar num único ponto o que muda na migração.

Esse único ponto é a **ingestão**: Kafka Connect Sink → ClickPipes. Como a ingestão
não transforma nada ([ADR-0002](../adr/0002-ingestao-via-clickhouse-kafka-connect-sink.md)),
L0, L1, L2 e L3 migram sem reescrita. É o principal argumento de redução de risco
da contratação, e a Fase 09 o testa.

---

## 2. Versões fixadas

*A preencher na Fase 01 (P01-T01, P01-T03).*

| Componente | Versão | Digest / tag | Onde está fixado |
|---|---|---|---|
| ClickHouse Server | | | `docker-compose.yml` |
| ClickHouse Keeper | | | `docker-compose.yml` |
| `clickhouse-kafka-connect` | | | `deploy/connect/Dockerfile` |
| Kafka (Confluent) | | | `docker-compose.yml` |
| Schema Registry | | | `docker-compose.yml` |

---

## 3. Pré-requisitos bloqueantes

**Gate da Fase 01 (P01-T04).** A coluna `LOCAL` é preenchida pelo
`make parity-check`. A coluna `CLOUD` só sai de `A VERIFICAR` **com evidência
citada** — doc oficial com link, ou teste próprio na Fase 09.

Se um destes não existir no Cloud, a arquitetura correspondente precisa de ADR novo
**antes** de continuar.

| # | Capacidade | Por que é bloqueante | ADR | LOCAL | CLOUD | Evidência |
|---|---|---|---|---|---|---|
| 1 | **Refreshable Materialized View** | é o mecanismo de **toda** a camada de marts; sem ela, correlação entre entidades mutáveis não se autocorrige | [0005](../adr/0005-estrategia-de-agregacao-mv-refreshable-dictionary.md) | `A VERIFICAR` | `A VERIFICAR` | |
| 2 | **KeeperMap** | state store do exactly-once do connector | [0002](../adr/0002-ingestao-via-clickhouse-kafka-connect-sink.md) | `A VERIFICAR` | `A VERIFICAR` | |
| 3 | **`ASOF JOIN`** | única forma nativa de correlação temporal (cenário 005) | [0005](../adr/0005-estrategia-de-agregacao-mv-refreshable-dictionary.md) | `A VERIFICAR` | `A VERIFICAR` | |
| 4 | **Dictionary `COMPLEX_KEY_HASHED`** | tira o join de cadastro do plano (cenários 002, 004) | [0005](../adr/0005-estrategia-de-agregacao-mv-refreshable-dictionary.md) | `A VERIFICAR` | `A VERIFICAR` | |
| 5 | **`ReplacingMergeTree(version, is_deleted)`** | dedup de CDC e delete lógico | [0004](../adr/0004-semantica-cdc-dedup-ordem-delete.md) | `A VERIFICAR` | `A VERIFICAR` | |
| 6 | **`PROJECTION`** | segundo padrão de acesso sem duplicar tabela (cenários 008, 010) | [0011](../adr/0011-serving-aplicacional-e-analitico-no-mesmo-store.md) | `A VERIFICAR` | `A VERIFICAR` | |
| 7 | **`ON CLUSTER`** | portabilidade do DDL sem reescrita | [0007](../adr/0007-migrations-sql-versionado.md) | `A VERIFICAR` | `A VERIFICAR` | |
| 8 | **Ingestão Kafka + Avro + Schema Registry gerenciada (ClickPipes)** | substitui o Connect na migração | [0002](../adr/0002-ingestao-via-clickhouse-kafka-connect-sink.md) | n/a | `A VERIFICAR` | |

### Restrições já conhecidas a confirmar

- ClickPipes com registry Confluent-compatível exige **HTTPS**.
- ClickPipes **não** suporta schema Avro com **referência externa**. O gerador do
  `dhctl` já não produz referência externa ([ADR-0006](../adr/0006-convencoes-avro-e-schema-registry.md) §5)
  — confirme que continua verdade.
- *Settings* experimentais são **restritos** no Cloud. O item 1 desta tabela é o que
  mais depende disso.

---

## 4. O que muda na migração

| Peça self-hosted | Equivalente no Cloud | Muda o modelo de dados? |
|---|---|---|
| Kafka Connect + `clickhouse-kafka-connect` | **ClickPipes** (ou connector gerenciado no Confluent Cloud) | **não** |
| ClickHouse Keeper próprio | gerenciado | não |
| `dh_landing` / `dh_core` / `dh_marts` / `dh_reports` | idênticos | não |
| Migrations com `{ON_CLUSTER}` | idênticas, com o cluster do Cloud | não (a verificar na Fase 09) |
| Contratos, `.avsc`, Refreshable MVs, dictionaries | idênticos | não |
| Roles e grants | idênticos | não |
| DLQ do Connect | tabela de erro do ClickPipes | não |

*Meta da Fase 09: zero migrations alteradas. Qualquer alteração é um achado.*

---

## 5. Insumos da cotação

*A preencher na Fase 08 (P08-T03, P08-T04), a partir de [`results.md`](results.md).*

| Insumo | Medido na PoC | Volume real estimado | Fator de extrapolação |
|---|---|---|---|
| Linhas por camada (L0/L1/L2) | | | |
| Bytes em disco por camada | | | |
| Razão de compressão por camada | | | |
| Taxa de ingestão sustentada (msg/s) | | | |
| CPU no pico de ingestão | | | |
| RAM no pico (ingestão e refresh) | | | |
| Duração do refresh por mart | | | |
| p95 de query aplicacional | | | |
| p95 de query analítica | | | |

**Extrapolação não é medição.** Qualquer coluna "volume real estimado" traz o fator
usado e como foi obtido.

---

## 6. Alternativas

*A preencher na Fase 08. Apresentar como comparativo, não como recomendação.*

| Critério | ClickHouse Cloud (GCP) | Self-hosted em GKE/GCE | Gerenciado por terceiro | Manter o estado atual |
|---|---|---|---|---|
| Esforço de operação | | | | |
| Risco técnico | | | | |
| O que a PoC já provou | | | | |
| O que continua em aberto | | | | |
| Insumos de custo | | | | |
| Portabilidade de saída | | | | |

---

## 7. Riscos residuais

*A preencher na Fase 08.*

| Risco | Impacto | O que o mudaria | Como mitigar |
|---|---|---|---|

---

## 8. O que a PoC não prova

Ver [`README.md`](README.md) → "O que a PoC deliberadamente não prova". Esta seção
é atualizada ao fim da Fase 08 e, se a Fase 09 for executada, alguns itens saem
dela.
