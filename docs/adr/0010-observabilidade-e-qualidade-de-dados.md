# ADR-0010 — Tratar qualidade de dados como saída de primeira classe, não como log

- **Status:** Accepted
- **Data:** 2026-09-13
- **Decisores:** Engenharia de Dados
- **ADRs relacionados:** 0002, 0004, 0005, 0008

## Contexto

Com CDC fora de ordem (ADR-0004), delete virando coluna, dedup assíncrono e
marts recalculados por janela (ADR-0005), a diferença entre "o pipeline está
funcionando" e "o número está certo" é enorme — e invisível sem medição.

Pior: os erros deste desenho são **plausíveis**. Um total 3% menor por causa de
`order_item` órfão não parece errado. Sem medição, a PoC pode "funcionar" e
levar a uma contratação baseada em números falsos.

## Decisão

### 1. Três classes de sinal, separadas

| Classe | Pergunta | Onde |
|---|---|---|
| **Liveness do pipeline** | os dados estão chegando? | `dh_meta.pipeline_health` |
| **Qualidade/corretude** | os dados estão certos? | `dh_meta.dq_check_results` |
| **Recurso/performance** | quanto custa? | `system.*` + `docs/evaluation/` |

### 2. Checks de qualidade são SQL versionado, com resultado gravado

Cada check é um arquivo em `internal/contexts/<ctx>/sql/40-reports/` ou
`internal/contexts/analytics/sql/40-reports/`, registrado em
`dh_meta.dq_checks(check_id, context, severity, query, threshold, description)`.
`dhctl dq run` executa todos e grava em
`dh_meta.dq_check_results(check_id, run_at, value, passed, details)`.

Severidade: `error` (quebra `make verify` e o critério de aceite da fase) ·
`warn` (registra e aparece no relatório) · `info`.

### 3. Catálogo mínimo de checks (obrigatório na Fase 08; alguns já na 05)

| ID | Classe | Regra | Severidade |
|---|---|---|---|
| `dq.landing.freshness` | liveness | idade da última mensagem por tópico < SLA do contrato | error |
| `dq.landing.dlq_depth` | liveness | DLQ vazia por connector | error |
| `dq.core.dup_key` | corretude | nenhuma chave duplicada em `v_*_current` | error |
| `dq.core.cdc_monotonic` | corretude | `_cdc_seq` não decresce por chave em L0 | warn |
| `dq.core.reconcile_count` | corretude | chaves distintas em L0 == chaves em `v_*_current` (+ deletadas) | error |
| `dq.core.deleted_leak` | corretude | nenhuma linha `_is_deleted=1` visível na view corrente | error |
| `dq.rel.orphan_rate` | corretude | % de `order_item` sem `order` < 0,5% | error |
| `dq.rel.orphan_age_p99` | corretude | idade p99 do órfão < janela de refresh do mart | error |
| `dq.rel.fk_violation` | corretude | toda `relation` declarada no contrato resolve | warn |
| `dq.marts.refresh_health` | liveness | `system.view_refreshes` sem exceção e `last_success` recente | error |
| `dq.marts.sum_parity` | corretude | soma do mart == soma calculada direto de `v_*_current` | error |
| `dq.dict.loaded` | liveness | `system.dictionaries.status = LOADED` e sem exceção | error |
| `dq.money.no_float` | corretude | nenhuma coluna monetária como `Float*` | error |

`dq.marts.sum_parity` é o check mais importante do projeto: é o que detecta
dupla contagem (ADR-0004 §6) comparando o agregado materializado com o cálculo
independente.

### 4. Orfandade é relatório, não erro

Por ADR-0004 §7, órfão transitório é normal. Então o que se mede é
**taxa** e **idade**, não presença. O cenário 009 expõe isso como relatório
consumível — a "observabilidade de dado" é entregável da PoC, não instrumentação
interna.

### 5. Métricas de recurso

Coletadas de `system.metrics`, `system.events`, `system.asynchronous_metrics`,
`system.query_log`, `system.parts`, `system.view_refreshes`,
`system.dictionaries`. Exportadas para Prometheus/Grafana (opcional, Fase 08) e
consolidadas em `docs/evaluation/results.md`, porque **são o insumo da cotação do
ClickHouse Cloud** (ADR-0008).

### 6. Logs estruturados em Go

`slog` em JSON, com `context`, `entity`, `topic`, `contract_hash`,
`migration_id`, `trace_id` quando aplicável. Sem PII de cliente em log — o
`producer` gera dado sintético, e a regra vale para não criar hábito errado.

## Alternativas consideradas

- **Great Expectations / Soda:** ricos, mas adicionam runtime Python e outro
  componente ao ambiente que a PoC quer manter enxuto e medível (ADR-0008).
  SQL versionado + tabela de resultado cobre o necessário aqui.
- **Só alerta no Grafana:** alerta não é resultado. Precisamos do histórico de
  `passed/value` para colocar no relatório da avaliação.
- **Checks só em CI (não em runtime):** não detectam degradação com dado real
  chegando, que é justamente o risco do CDC.

## Consequências

### Positivas
- "Funcionou" passa a ser afirmação verificável.
- `sum_parity` transforma a armadilha mais perigosa do projeto em teste automático.
- O relatório de avaliação nasce com números medidos, não estimados.

### Negativas / custo aceito
- Checks custam query; `dhctl dq run` roda por agendamento, não a cada insert.
- Thresholds iniciais são arbitrários e precisarão de calibração com dado real.
  Estão versionados para que a calibração seja um diff revisável.

## Impacto no repositório

`db/shared/00-bootstrap/` (`dh_meta`), `internal/platform/observability/`,
`cmd/dhctl` (`dq run`), `*/sql/40-reports/`, `docs/evaluation/`,
`docs/scenarios/009-*.md`, `docs/runbooks/troubleshooting-ingestion.md`.

## Como validar

```bash
dhctl dq run --severity error     # exit 0
make verify                        # inclui dq run
```
```sql
SELECT check_id, value, passed, run_at FROM dh_meta.dq_check_results
WHERE run_at > now() - INTERVAL 1 HOUR ORDER BY passed, check_id;
```
