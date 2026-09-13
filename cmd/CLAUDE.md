# CLAUDE.md — cmd/

## Regra

`cmd` é **fino**: parsing de flag, leitura de configuração, wiring e tratamento de
sinal. Toda lógica mora em `internal/`. Se um arquivo aqui passa de ~150 linhas,
provavelmente há lógica que deveria estar em `internal/`.

## Os três binários

### `dhctl` — a plataforma

O motor de extensibilidade. Subcomandos:

| Subcomando | O que faz | Fase |
|---|---|---|
| `contract validate [paths]` | valida descritores | 02 |
| `contract list` | lista as entidades | 02 |
| `contract import --from-subject` | rascunho de contrato a partir do Schema Registry | 02 |
| `generate [--only=...] [--check]` | gera avsc, DDL de landing, connector, tópicos, esqueleto de core | 02 |
| `migrate [--status] [--dry-run]` | aplica migrations na ordem canônica | 02 |
| `core rebuild [--entity] [--all]` | reconstrói L1 a partir de L0 | 05 |
| `topics apply [--dry-run]` / `topics list` | provisiona tópicos | 03 |
| `schemas apply [--dry-run]` / `schemas check-compat` | registra e testa schemas | 03 |
| `connectors apply/status/pause/resume/restart/delete` | gerencia connectors | 04 |
| `connectors reset-state` | limpa o estado do KeeperMap para permitir replay | 04 |
| `mart refresh --mart [--window]` / `mart status` | opera as Refreshable MVs | 06 |
| `reports run [--scenario]` | executa as queries dos cenários | 06 |
| `dq run [--severity] [--check]` / `dq list` | checks de qualidade | 08 |
| `metrics snapshot` | métricas de recurso | 08 |

### `producer` — a origem da PoC

A PoC não tem o SAP. Este binário **é** a origem, e a qualidade dele determina se os
critérios de aceite significam algo. Produz Avro no wire format do Confluent,
incluindo as anomalias deliberadas (fora de ordem, delete, ressurreição, órfão,
cupom fantasma, SKU sem preço).

Flags principais: `--seed`, `--scale=smoke|dev|bench`, `--entities`, `--adverse`,
`--rate`, `--dry-run`, `--limit`, `--inject-malformed`.

**A mesma semente produz o mesmo dataset.** Sem isso, benchmark não é comparável e
teste e2e é instável.

### `api` — o serving aplicacional

Prova o requisito "servir aplicações". Conecta como role `dh_app`
(`max_execution_time=3`, `readonly=1`).

| Rota | Fonte |
|---|---|
| `GET /orders/{order_id}/360` | `dh_marts.order_360` |
| `GET /customers/{customer_id}/metrics` | `dh_marts.v_customer_metrics` |
| `GET /health` | liveness do processo |
| `GET /health/data` | `dh_reports.v_dq_alerts` (`503` se houver alerta `error`) |

**Não existe endpoint que aceite SQL do cliente**
([ADR-0011](../docs/adr/0011-serving-aplicacional-e-analitico-no-mesmo-store.md) §5).
Cada endpoint mapeia para uma query nomeada e versionada em
`internal/contexts/*/reports/`. Toda resposta de dado traz `X-Data-Freshness` e
`X-Data-Source`.

## Regras

1. Configuração **só** por env, via `internal/platform/config`. Nada de flag com
   segredo.
2. `SIGINT`/`SIGTERM` encerram com flush (o `producer` perdendo mensagem no
   encerramento viraria "bug de ingestão" na fase seguinte).
3. Exit code significativo: `0` sucesso, `1` falha de negócio/validação,
   `2` erro de uso.
4. Toda operação destrutiva pede confirmação, com `--yes` para automação.
