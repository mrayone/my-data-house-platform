# Resultados medidos

**Status:** esqueleto. Preenchido nas Fases 06 a 08.

Regras (ver [`README.md`](README.md)): todo número tem **fonte** (comando ou query)
e **data**. Onde não houver medição, escreva **"não medido"** — nunca uma
estimativa apresentada como número.

---

## 0. Ambiente da medição

| Item | Valor |
|---|---|
| Data | |
| ClickHouse (versão / digest) | |
| Plugin do connector (versão) | |
| Host: CPU / RAM / disco | |
| Docker / Compose | |
| Semente do dataset (`--seed`) | |
| Escala (`--scale`) | |

---

## 1. Ingestão (Fase 08, P08-T03 bloco 1)

| Métrica | `exactlyOnce=true` | `exactlyOnce=false` | Fonte |
|---|---|---|---|
| msg/s agregado | | | |
| msg/s por tópico (mín / máx) | | | |
| Lag máximo do consumer group | | | |
| CPU do ClickHouse no pico | | | |
| CPU do Connect no pico | | | |
| RAM do ClickHouse no pico | | | |

## 2. Latência L0 → L1 (bloco 2)

| Percentil | Latência | Fonte |
|---|---|---|
| p50 | | |
| p95 | | |
| p99 | | |

## 3. Refresh dos marts (bloco 3, e Fases 06-07)

| Mart | Mecanismo | Intervalo | Janela | Duração | RAM no pico | Fonte |
|---|---|---|---|---|---|---|
| `order_360` | Refreshable MV | 1 min | 3 d | | | |
| `revenue_by_bu_day` | Refreshable MV | 15 min | 3 d | | | |
| `payment_funnel_day` | Refreshable MV | 5 min | 3 d | | | |
| `discount_effectiveness` | Refreshable MV | 15 min | total | | | |
| `item_margin_daily` (ASOF) | Refreshable MV | 15 min | 7 d | | | |
| `stock_coverage_dc_item` | Refreshable MV | 15 min | total | | | |
| `stockout_candidates` | Refreshable MV | 15 min | total | | | |
| `customer_metrics` | Refreshable MV | 30 min | **sem janela** | | | |
| `data_quality_overview` | Refreshable MV | 5 min | 7 d | | | |
| `idle_catalog_items` | Refreshable MV | 60 min | total | | | |

*`customer_metrics` e `idle_catalog_items` são os dois refreshes mais caros e os que
decidem se a estratégia escala.*

## 4. Dictionaries (Fase 06, P06-T01)

| Dictionary | `element_count` | `bytes_allocated` | Tempo de carga | Fonte |
|---|---|---|---|---|
| `dict__business_unit` | | | | |
| `dict__discount_codes` | | | | |

## 5. Queries aplicacionais (bloco 4)

| Endpoint | Conexões | p50 | p95 | p99 | Fonte |
|---|---|---|---|---|---|
| `GET /orders/{id}/360` | 1 | | | | |
| `GET /orders/{id}/360` | 10 | | | | |
| `GET /orders/{id}/360` | 50 | | | | |
| `GET /customers/{id}/metrics` | 1 | | | | |
| `GET /customers/{id}/metrics` | 10 | | | | |
| `GET /customers/{id}/metrics` | 50 | | | | |

*Alvo: p95 < 50 ms (ADR-0011).*

## 6. Queries analíticas (bloco 5)

| Cenário | Query | Duração | Linhas lidas | Bytes lidos | Fonte |
|---|---|---|---|---|---|
| 001 | | | | | |
| 002 | | | | | |
| 003 | | | | | |
| 004 | | | | | |
| 005 | | | | | |
| 006 | | | | | |
| 007 | | | | | |
| 008 | | | | | |
| 009 | | | | | |
| 010 | | | | | |

## 7. `FINAL` vs `argMax` (bloco 6)

Custo da view corrente obrigatória ([ADR-0004](../adr/0004-semantica-cdc-dedup-ordem-delete.md) §4).

| Leitura | Forma | Duração | CPU | Fonte |
|---|---|---|---|---|
| chave única | `FINAL` | | | |
| chave única | `argMax` | | | |
| varredura ampla | `FINAL` | | | |
| varredura ampla | `argMax` | | | |

## 8. Storage (bloco 7)

| Camada | Tabelas | Linhas | Bytes em disco | Bytes descomprimidos | Razão | Parts ativos |
|---|---|---|---|---|---|---|
| `dh_landing` | | | | | | |
| `dh_core` | | | | | | |
| `dh_marts` | | | | | | |
| **Total** | | | | | | |

## 9. Interferência entre perfis (bloco 8)

Risco declarado no [ADR-0011](../adr/0011-serving-aplicacional-e-analitico-no-mesmo-store.md):
isolamento imperfeito em single-node.

| Cenário | p95 aplicacional | Degradação | Fonte |
|---|---|---|---|
| só carga aplicacional | | — | |
| aplicacional + analítica simultâneas | | | |

## 10. Extensibilidade

Ver [`extensibility-drill.md`](extensibility-drill.md).

## 11. Qualidade de dados

| check_id | Valor | Threshold | Passou | Fonte |
|---|---|---|---|---|

*Preencher com o último `dhctl dq run` da Fase 08.*

---

## Comparativo self-hosted vs Cloud

*Somente se a Fase 09 for executada.*

| Métrica | Self-hosted (PoC) | Cloud (GCP) | Observação |
|---|---|---|---|
