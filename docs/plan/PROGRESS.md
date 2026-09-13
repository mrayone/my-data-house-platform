# PROGRESS

**Estado atual do projeto.** Este arquivo é a fonte da verdade sobre o que já foi
feito. Todo agente executor **lê este arquivo antes de começar** e **atualiza no
mesmo commit** de cada tarefa concluída.

- Plano: [`implementation-plan.md`](implementation-plan.md)
- Regras de trabalho e commit: [`../../CLAUDE.md`](../../CLAUDE.md) §5

---

## Resumo executivo

> Preencha ao fim da Fase 08, em no máximo 10 linhas: o que a PoC provou, o que não
> provou, e qual é a recomendação de próximo passo.

**Status geral:** documentação e estruturas iniciais concluídas. Implementação não
iniciada.

| Fase | Status | Branch | Concluída em | Observação |
|---|---|---|---|---|
| Fundação (docs + estrutura) | **DONE** | `docs/foundation` | 2026-09-13 | ADRs, arquitetura, 10 cenários, contratos, plano |
| [00 — Bootstrap do repositório](phases/phase-00-repo-bootstrap.md) | `TODO` | | | |
| [01 — Ambiente local + gate de paridade](phases/phase-01-local-environment.md) | `TODO` | | | **P01-T04 é gate: se falhar, pare** |
| [02 — Contratos, codegen e migrations](phases/phase-02-contracts-and-dhctl.md) | `TODO` | | | fase mais densa |
| [03 — Tópicos, schemas e producer](phases/phase-03-provisioning-and-producer.md) | `TODO` | | | |
| [04 — Landing e ingestão](phases/phase-04-landing-ingestion.md) | `TODO` | | | |
| [05 — Camada core](phases/phase-05-core-layer.md) | `TODO` | | | onde a corretude se decide |
| [06 — Marts 001-005](phases/phase-06-marts-part-1.md) | `TODO` | | | paralelizável por cenário |
| [07 — Marts 006-010, API, extensibilidade](phases/phase-07-marts-part-2-and-api.md) | `TODO` | | | |
| [08 — Qualidade, benchmark, avaliação](phases/phase-08-observability-and-evaluation.md) | `TODO` | | | entregável final |
| [09 — Validação no Cloud GCP](phases/phase-09-clickhouse-cloud-validation.md) | `OPCIONAL` | | | só com decisão de abrir trial |

Status possíveis: `TODO` · `IN PROGRESS` · `BLOCKED` · `DONE` · `OPCIONAL`

---

## Como registrar progresso

Ao concluir uma tarefa, edite a tabela da fase correspondente abaixo:

```
| P02-T03 | DONE | 2026-09-20 | a1b2c3d | gerador de DDL; 8 arquivos gerados, golden tests ok |
```

Ao concluir a fase, atualize também a linha dela na tabela de resumo acima.

Se algo bloquear, registre em [Bloqueios](#bloqueios) **com o erro literal** e
**pare** — não improvise workaround (`CLAUDE.md` §5 e `implementation-plan.md`).

---

## Fase 00 — Bootstrap do repositório

| Tarefa | Status | Data | Commit | Observação |
|---|---|---|---|---|
| P00-T01 — módulo Go e árvore de pacotes | `DONE` | 2026-09-13 | de414a5 | módulo `github.com/mrayone/my-data-house-platform`; go 1.27.1; logging/config/version + 3 `cmd/*` + `doc.go` dos 6 contextos |
| P00-T02 — `Makefile` | `TODO` | | | |
| P00-T03 — checks de fronteira de contexto | `TODO` | | | |
| P00-T04 — CI e `CLAUDE.md` por contexto | `TODO` | | | |

## Fase 01 — Ambiente local e gate de paridade

| Tarefa | Status | Data | Commit | Observação |
|---|---|---|---|---|
| P01-T01 — ClickHouse e Keeper | `TODO` | | | registrar versão fixada |
| P01-T02 — Kafka e Schema Registry | `TODO` | | | |
| P01-T03 — Kafka Connect com plugin | `TODO` | | | registrar versão do plugin |
| P01-T04 — **GATE** de paridade | `TODO` | | | Refreshable MV + KeeperMap |
| P01-T05 — runbook do ambiente local | `TODO` | | | |

## Fase 02 — Contratos, codegen e migrations

| Tarefa | Status | Data | Commit | Observação |
|---|---|---|---|---|
| P02-T01 — parse e validação de contratos | `TODO` | | | |
| P02-T02 — gerador de Avro | `TODO` | | | |
| P02-T03 — gerador de DDL de landing | `TODO` | | | |
| P02-T04 — gerador de connector e tópicos | `TODO` | | | |
| P02-T05 — `dhctl generate --check` | `TODO` | | | |
| P02-T06 — runner de migrations | `TODO` | | | |
| P02-T07 — checks de SQL e runbook de novo tópico | `TODO` | | | |

## Fase 03 — Tópicos, schemas e producer

| Tarefa | Status | Data | Commit | Observação |
|---|---|---|---|---|
| P03-T01 — Schema Registry e `schemas apply` | `TODO` | | | |
| P03-T02 — provisionamento de tópicos | `TODO` | | | |
| P03-T03 — producer Avro genérico | `TODO` | | | |
| P03-T04 — geradores de domínio | `TODO` | | | IDs nomeados são contrato com as fases 05-08 |
| P03-T05 — `cmd/producer` e alvos `seed` | `TODO` | | | |

## Fase 04 — Landing e ingestão

| Tarefa | Status | Data | Commit | Observação |
|---|---|---|---|---|
| P04-T01 — aplicador de connectors | `TODO` | | | |
| P04-T02 — `make bootstrap` | `TODO` | | | |
| P04-T03 — e2e de ingestão e exactly-once | `TODO` | | | |
| P04-T04 — e2e de DLQ | `TODO` | | | |
| P04-T05 — troubleshooting e reset de estado | `TODO` | | | armadilha do KeeperMap |

## Fase 05 — Camada core

| Tarefa | Status | Data | Commit | Observação |
|---|---|---|---|---|
| P05-T01 — tabelas, MVs e views correntes | `TODO` | | | 8 entidades |
| P05-T02 — rebuild de L1 a partir de L0 | `TODO` | | | |
| P05-T03 — checks estáticos de corretude | `TODO` | | | 5 scripts |
| P05-T04 — e2e da semântica CDC | `TODO` | | | prova da fase |
| P05-T05 — runbook de reprocesso | `TODO` | | | |

## Fase 06 — Marts 001-005

| Tarefa | Status | Data | Commit | Observação |
|---|---|---|---|---|
| P06-T01 — dictionaries de cadastro | `TODO` | | | pré-requisito de T03 e T05 |
| P06-T02 — cenário 001 `order_360` | `TODO` | | | |
| P06-T03 — cenário 002 `revenue_by_bu_day` | `TODO` | | | |
| P06-T04 — cenário 003 `payment_funnel_day` | `TODO` | | | |
| P06-T05 — cenário 004 `discount_effectiveness` | `TODO` | | | |
| P06-T06 — cenário 005 `item_margin_daily` (ASOF) | `TODO` | | | tarefa mais delicada |
| P06-T07 — operação de marts e e2e de late arrival | `TODO` | | | |

## Fase 07 — Marts 006-010, API e extensibilidade

| Tarefa | Status | Data | Commit | Observação |
|---|---|---|---|---|
| P07-T01 — cenário 006 `stock_coverage_dc_item` | `TODO` | | | |
| P07-T02 — cenário 007 `stockout_candidates` | `TODO` | | | anti-join: `= ''`, não `IS NULL` |
| P07-T03 — cenário 008 `customer_metrics` | `TODO` | | | sem janela, de propósito |
| P07-T04 — cenário 009 painel de qualidade gerado | `TODO` | | | SQL gerado dos contratos |
| P07-T05 — cenário 010 `idle_catalog_items` | `TODO` | | | `FULL OUTER` |
| P07-T06 — API de serving | `TODO` | | | p95 < 50 ms |
| P07-T07 — **prova de extensibilidade** | `TODO` | | | 2 arquivos, 0 Go, < 30 min |

## Fase 08 — Qualidade, benchmark e avaliação

| Tarefa | Status | Data | Commit | Observação |
|---|---|---|---|---|
| P08-T01 — catálogo de checks e `dq run` | `TODO` | | | |
| P08-T02 — métricas de recurso | `TODO` | | | insumo da cotação |
| P08-T03 — benchmark | `TODO` | | | 8 blocos |
| P08-T04 — relatório de avaliação | `TODO` | | | entregável final; não se autoaprova |
| P08-T05 — fechamento de docs e dívidas | `TODO` | | | |

## Fase 09 — Validação no Cloud (opcional)

| Tarefa | Status | Data | Commit | Observação |
|---|---|---|---|---|
| P09-T01 — migrations no Cloud | `OPCIONAL` | | | meta: 0 migrations alteradas |
| P09-T02 — ClickPipes | `OPCIONAL` | | | |
| P09-T03 — revalidar cenários e benchmark | `OPCIONAL` | | | |
| P09-T04 — fechar a avaliação | `OPCIONAL` | | | |

---

## Bloqueios

Registre aqui tudo que impediu a conclusão de uma tarefa. Um bloqueio registrado é
progresso; um workaround escondido é dívida.

| # | Tarefa | Data | Descrição | Erro literal | Status |
|---|---|---|---|---|---|
| — | — | — | nenhum registrado | — | — |

Status: `ABERTO` · `EM ANÁLISE` · `RESOLVIDO` (com o commit que resolveu) ·
`ACEITO` (virou dívida em `docs/TECH-DEBT.md`)

---

## Decisões tomadas durante a execução

Toda vez que a implementação divergir de um ADR, registre aqui **e** escreva o ADR
novo. Nunca edite o ADR aceito.

| Data | Tarefa | Decisão | ADR novo | Motivo |
|---|---|---|---|---|
| — | — | — | — | — |

---

## Histórico da fundação

| Data | Entrega |
|---|---|
| 2026-09-13 | 11 ADRs (0001-0011) + índice e template |
| 2026-09-13 | `docs/architecture/`: overview, modelo em camadas, fluxo de dados, extensibilidade |
| 2026-09-13 | `docs/scenarios/`: 10 cenários especificados + índice + template |
| 2026-09-13 | `contracts/`: JSON Schema + 8 descritores validados |
| 2026-09-13 | `docs/plan/`: plano mestre + 10 arquivos de fase + este PROGRESS |
| 2026-09-13 | `CLAUDE.md` raiz e por pasta; estrutura de pastas context-first |
