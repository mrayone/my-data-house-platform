# Spike — teste de fluxo mock: landing → core → marts contra ClickHouse real

**Branch:** `spike/mock-flow-clickhouse-kafka` · **Data:** 2026-09-13

Este documento registra um spike de validação: gerar dados sintéticos para as
8 entidades da PoC e provar, contra um ClickHouse **real** (não mockado), que
o desenho de landing → core → marts descrito nos ADRs e nos cenários
funciona como especificado. Não é o entregável formal das Fases 02/03/05-08
— é evidência de que o desenho é executável antes de investir nessas fases.

## O que este spike **prova** e o que **não prova**

**Prova (com ClickHouse real, versão 26.8.3.105):**
- Dedup por `ReplacingMergeTree(_cdc_seq, _is_deleted)` + `FINAL` independente
  da ordem de chegada.
- Semântica de delete (`_is_deleted`) e ressurreição.
- Detecção de órfão via anti-join (`LEFT JOIN` + `= ''`, não `IS NULL`).
- `ASOF JOIN` para correlação temporal preço-no-instante-da-venda.
- `Dictionary` `COMPLEX_KEY_HASHED` com `dictGet`/`dictGetOrDefault`/`dictHas`.
- Duas `Refreshable MATERIALIZED VIEW` (`revenue_by_bu_day`,
  `payment_funnel_day`) executando refresh sob demanda (`SYSTEM REFRESH VIEW`)
  e produzindo números agregados corretos.
- Fan-out 1:N, retentativa e latência com nulo no funil de pagamento.

**NÃO prova (fora do escopo deste spike):**
- O transporte real Avro + Kafka + Confluent Schema Registry + Kafka Connect
  Sink. Não há um broker Kafka alcançável neste ambiente de execução (mirrors
  do Apache Kafka/Confluent bloqueados pelo proxy de saída; ver Bloqueio #3 em
  `docs/plan/PROGRESS.md`). Os dados foram inseridos via
  `clickhouse-client ... FORMAT JSONEachRow`, como stand-in explícito.
- O `dhctl generate` real orientado a contrato (Fase 02). O DDL foi gerado por
  um script Python descartável (`gen_ddl.py`, fora do repositório) que segue
  os mesmos contratos YAML e o mesmo layout do ADR-0004, mas **não é** o
  gerador Go da Fase 02.
- Docker Compose (Fase 01, tarefas T01-T03) — ClickHouse rodou como binário
  standalone (`ch-runtime/`, baixado dos releases do GitHub
  `ClickHouse/ClickHouse` `v26.8.3.105-lts`), não via `docker compose up`,
  porque este ambiente de execução não tem Docker (Bloqueio #3).
- `ORD-LATE-1`/`ORD-LATE-2` (o timing real de correção assíncrona pelo
  refresh) — o carregamento aqui é um batch único, então ambos os pedidos já
  aparecem resolvidos na mesma consulta de verificação. O comportamento de
  "órfão transitório que se autocorrige no próximo refresh" (cenário 009)
  precisa de um teste com streaming real e passagem de tempo.

## Ambiente

| Item | Valor |
|---|---|
| ClickHouse | `26.8.3.105` standalone (binário de release, não Docker) |
| Keeper | `clickhouse-keeper` standalone, single-node |
| Dados | gerados por `./bin/producer mock --out ./out/mock --base 2026-09-13T12:00:00Z` |
| Contratos aplicados | os 8 de `contracts/domains/**/*.yaml` |
| Camadas aplicadas | `dh_landing` (8 tabelas), `dh_core` (8 tabelas + 8 MVs + 8 views), `dh_core` (2 dictionaries), `dh_marts` (2 tabelas + 2 Refreshable MV + 2 views) |
| Registros carregados | 69 (organization: 3, pricing: 7, customer: 3, inventory: 4, sales.order: 18, sales.order_item: 12, sales.order_payment: 22) |

## Matriz de verificação

| # | Cenário / ADR | Query | Resultado | Veredito |
|---|---|---|---|---|
| 1 | Dedup fora de ordem (`ORD-OOO-1`, ADR-0004 §2) | `SELECT ... FROM v_sales__order_current WHERE order_id='ORD-OOO-1'` | `status=canceled, _cdc_seq=7` (a versão de maior `_cdc_seq`, mesmo chegando primeiro no arquivo) | **PASS** |
| 2 | Delete de CDC (`ORD-DEL-1`, ADR-0004 §5) | `count()` na view corrente | `0` | **PASS** |
| 3 | Ressurreição (`ORD-RES-1`) | status na view corrente | `delivered` | **PASS** |
| 4 | Órfão permanente (`ORD-ORPHAN`, cenário 009) | anti-join `order_item` → `order` | 1 linha órfã, exatamente `ORD-ORPHAN` | **PASS** |
| 5 | Late arrival (`ORD-LATE-1/2`) | mesmo anti-join | ambos resolvidos (pedido e item presentes) — ver ressalva acima | **PASS** (com ressalva de escopo) |
| 6 | Normalização de cupom (cenário 004) | `dictGetOrDefault` em `dict__discount_codes` | `PROMO10` → `percent`; `promo10`, `" PROMO10 "`, `CUPOM-INEXISTENTE` → `SEM_CADASTRO` | **PASS** (achado exposto, não escondido — como o desenho pede) |
| 7 | `dictGet`/`dictHas` (`dict__business_unit`) | `dictGet(...,'bu_name',...)` / `dictHas` | `Centauro Digital`; `0` para BU inexistente | **PASS** |
| 8 | `ASOF JOIN` temporal (cenário 005) | item×preço vigente no instante do pedido | pedidos antes de `priceChangeAt` casam com o preço antigo (`199.9`/`89.9`); depois, com o novo (`219.9`/`99.9`) | **PASS** |
| 9 | Lookup composto `item_id+dc_id` (`stock_position`) | `LEFT JOIN` com `dc_id` nulável | `DC-02` abaixo do `safety_stock` detectado; `dc_id` nulo em `ORD-000126` corretamente sem posição | **PASS** |
| 10 | Fan-out 1:N + dedup de pagamento (`ACQ-TEST`, cenário 003) | `Refreshable MV payment_funnel_day` | `attempts=1, attempts_captured=1` (não 3, apesar de 3 versões em L0) | **PASS** |
| 11 | Latência com nulo (`ACQ-LAT-TEST`) | idem | `auth_latency_p50_s=10` (não ~0) | **PASS** |
| 12 | Retentativa (`ACQ-RETRY-TEST`) | idem | `attempts=3, orders_with_attempt=1, max_attempts_per_order=3, orders_with_retry=1` | **PASS** |
| 13 | Refreshable MV de receita (cenário 002) | `v_revenue_by_bu_day` após `SYSTEM REFRESH VIEW` | 4 linhas agregadas por BU/dia, condizentes com os pedidos carregados | **PASS** |

## Bugs encontrados e corrigidos durante o spike

1. `internal/contexts/organization/sql/30-marts/0010__dict_business_unit.sql` e
   `.../pricing/.../0010__dict_discount_codes.sql` foram escritos inicialmente
   em `dh_marts`; o padrão correto (ADR-0005, cenários 002/004) é
   `dh_core.dict__*`. Corrigido antes deste teste.
2. `internal/contexts/analytics/sql/30-marts/0020__revenue_by_bu_day.sql`
   estava sem a `CREATE VIEW dh_marts.v_revenue_by_bu_day` final — só tinha a
   tabela e a Refreshable MV. Sem essa view, nada quebra na aplicação do DDL
   (por isso passou despercebido), mas a leitura documentada no cenário 002
   (`SELECT ... FROM v_revenue_by_bu_day`) falhava com `UNKNOWN_TABLE`.
   Corrigido neste spike.
3. O gerador inicial (`sales/generator/order_payment.go`) colocava o
   `created_at` de `ACQ-TEST` 10 dias no passado. A `Refreshable MV` do
   cenário 003 filtra por `toDate(created_at) >= today() - 3` (janela de
   recálculo de 3 dias, por desenho) — a tentativa ficava fora da janela e o
   mart não a mostrava. Não é bug do ClickHouse nem do desenho: é um erro de
   dado do gerador. Corrigido movendo `ACQ-TEST` para 1 dia no passado.

## Reprodução

```bash
export PATH="$HOME/toolchain/bin:$PATH" GOTOOLCHAIN=local   # Go 1.27+
go build -o bin/producer ./cmd/producer
./bin/producer mock --out ./out/mock --base 2026-09-13T12:00:00Z

# ClickHouse standalone (ou docker compose up, se disponível):
clickhouse-keeper --config-file=<keeper.xml> &
clickhouse-server --config-file=<server.xml> &

# aplica bootstrap + landing + core + marts (ON_CLUSTER vazio para single-node)
# depois faz um INSERT ... FORMAT JSONEachRow por arquivo de out/mock/*.jsonl
# nas tabelas dh_landing correspondentes.
```

O script completo usado neste spike (limpeza, subida dos binários, aplicação
de DDL, carga e as 13 verificações acima) não foi commitado — é infraestrutura
de teste local (caminhos absolutos do ambiente de execução), não parte do
repositório. Os artefatos permanentes deste spike são os arquivos de código e
SQL commitados nesta branch (geradores em `internal/contexts/*/generator/`,
`internal/platform/mockgen/`, `internal/platform/mockrun/`, `cmd/producer mock`
e o DDL em `internal/contexts/*/sql/` e `db/shared/00-bootstrap/`).

## Próximos passos (fora deste spike)

- Fase 01 (T01-T03): validar o `docker-compose.yml` já commitado em
  `feat/phase-01-local-environment` numa máquina com Docker (bloqueio #3).
- Fase 02: substituir `gen_ddl.py` pelo `dhctl generate` real em Go, usando
  este spike como oráculo de comparação (mesma saída para os mesmos
  contratos).
- Fase 03: produtor real em Avro contra Kafka/Confluent + Schema Registry,
  substituindo `producer mock` (que continua útil como fonte de dados de
  teste determinística, independente de Kafka).
- Testar `ORD-LATE-1`/`ORD-LATE-2` com um harness que controle a ordem real
  de chegada entre tópicos e observe o refresh corrigindo o órfão transitório
  ao vivo (cenário 009) — não reproduzível com carga em lote único.
