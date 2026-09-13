# ADR-0011 — Servir aplicação e análise do mesmo ClickHouse, separando por objeto e chave de ordenação

- **Status:** Accepted
- **Data:** 2026-09-13
- **Decisores:** Engenharia de Software, Engenharia de Dados
- **ADRs relacionados:** 0001, 0004, 0005, 0010

## Contexto

O requisito é explícito: agregar *"informações que sirvam para aplicações e
também tenhamos a possibilidade de dados analíticos"*. São dois perfis de acesso
antagônicos:

| | Aplicacional | Analítico |
|---|---|---|
| Acesso | por chave (`order_id`, `customer_id`) | varredura com filtro de data |
| Linhas lidas | unidades | milhões |
| Latência aceitável | dezenas de ms | segundos |
| Concorrência | alta | baixa |
| Frescor | o mais recente possível | minutos aceitáveis |

Um ClickHouse com `ORDER BY` pensado só para análise serve mal a aplicação, e o
inverso também. Mas manter dois stores dobra o custo e a superfície — e a PoC
existe para responder se **um** store basta.

## Decisão

**Um store, objetos distintos por perfil de acesso.** A separação não é física;
é de modelagem, de chave de ordenação e de permissão.

### 1. `ORDER BY` é escolhido pelo perfil dominante do objeto

| Objeto | Perfil | `ORDER BY` |
|---|---|---|
| `dh_core.sales__order` | aplicacional | `(order_id)` |
| `dh_core.sales__order_item` | aplicacional | `(order_id, item_id)` |
| `dh_core.inventory__stock_position` | aplicacional | `(item_id, dc_id)` |
| `dh_marts.order_360` | aplicacional | `(order_id)` |
| `dh_marts.revenue_by_bu_day` | analítico | `(business_unit_code, order_date)` |
| `dh_marts.customer_metrics` | analítico | `(customer_id)` |

Regra: **a primeira coluna do `ORDER BY` é aquela pela qual o acesso dominante
filtra.** Requisitos de acesso conflitantes no mesmo dado resolvem-se por
`PROJECTION`, nunca duplicando a tabela à mão.

### 2. `PROJECTION` para o segundo padrão de acesso

Quando o mesmo mart precisa ser lido por duas chaves (ex.: `order_360` por
`order_id` **e** por `customer_id`), a segunda chave entra como projection:

```sql
ALTER TABLE dh_marts.order_360 ADD PROJECTION IF NOT EXISTS p_by_customer
( SELECT * ORDER BY (customer_id, order_created_at) );
```

Projection custa storage e escrita; cada uma exige justificativa no doc do mart
e é limitada a **duas por tabela** sem novo ADR.

### 3. A aplicação nunca consulta L0 nem L1 direto

- Aplicação lê **`dh_marts`** e **`dh_reports`**.
- Acesso pontual a entidade isolada usa a view `v_<entidade>_current`
  (ADR-0004 §4), nunca a tabela.
- Enforcement por grant: o role `dh_app` recebe `SELECT` em `dh_marts` e
  `dh_reports` e **nada** em `dh_landing`; em `dh_core`, só nas views `v_*`.
  Roles em `db/shared/00-bootstrap/`.

### 4. Perfis de usuário separam consumo de recurso

Três roles, com settings próprios em `deploy/clickhouse/users/`:

| Role | Uso | Settings-chave |
|---|---|---|
| `dh_app` | API aplicacional | `max_execution_time=3`, `max_threads` baixo, `max_result_rows` limitado, `readonly=1`, `final=1` |
| `dh_analyst` | BI/ad-hoc | `max_execution_time=60`, `max_memory_usage` maior, `readonly=1` |
| `dh_pipeline` | dhctl/connector | DDL e INSERT nos databases `dh_*` |

`max_execution_time` curto no `dh_app` é intencional: query aplicacional lenta
deve **falhar rápido e virar bug**, não degradar o cluster.

### 5. A API expõe contrato de consulta, não SQL

`cmd/api` expõe endpoints nomeados por caso de uso
(`GET /orders/{id}/360`, `GET /reports/revenue-by-bu`), cada um mapeado para uma
query versionada em `internal/contexts/<ctx>/reports/`. **Não há endpoint que
aceite SQL do cliente.** Evita que o perfil de acesso escape do desenho e mantém
o `ORDER BY` significativo.

### 6. Frescor é declarado por endpoint

Cada endpoint documenta sua fonte e o frescor esperado (ex.: `order_360` =
intervalo de refresh da Refreshable MV, ADR-0005). Quando o caso exigir "agora",
o endpoint lê de `v_*_current` com `FINAL` e paga o custo — decisão explícita por
endpoint, registrada no doc do cenário.

## Alternativas consideradas

### A) Dois stores: OLTP/KV para aplicação + ClickHouse para análise
- **Prós:** cada um no seu ponto ótimo; latência aplicacional previsível.
- **Contras:** dobra o custo e a operação, e introduz sincronização entre stores
  (mais um lugar para divergir). Principalmente: **responde outra pergunta.** A
  PoC quer saber se o ClickHouse serve os dois casos.
- **Por que não agora:** é o plano B, e a PoC gera exatamente os números
  (latência p95/p99 por endpoint) que justificariam recorrer a ele. Registrado em
  `docs/evaluation/`.

### B) Tabelas duplicadas por chave de acesso
- **Prós:** cada acesso com sua chave ótima, sem custo de projection em leitura.
- **Contras:** duas tabelas para manter coerentes, com duas MVs — e divergência
  silenciosa.
- **Por que não:** `PROJECTION` resolve com garantia de coerência pelo engine.

### C) Expor SQL direto para as aplicações
- **Prós:** flexibilidade total, zero backend.
- **Contras:** qualquer query de aplicação pode varrer tudo; sem contrato, o
  `ORDER BY` perde sentido; acoplamento total ao schema físico.
- **Por que não:** inviabiliza evoluir L1/L2 sem quebrar cliente.

## Consequências

### Positivas
- Um store para operar, avaliar e cotar.
- Grants e perfis impedem estruturalmente o acesso errado.
- Latência p95/p99 por endpoint vira número medido da avaliação (ADR-0010).

### Negativas / custo aceito
- `FINAL` no caminho aplicacional custa CPU; por isso o caminho preferido é ler
  do mart já materializado.
- Projections aumentam storage e tempo de insert.
- `max_execution_time=3` vai causar falha em query mal escrita — é o objetivo,
  mas exige disciplina ao adicionar endpoint.
- Isolamento de recurso por role em single-node é imperfeito; carga analítica
  pesada afeta a aplicacional. Medir em `make bench` e registrar como risco na
  avaliação (no Cloud, resolve-se com serviços/warehouses separados).

## Impacto no repositório

`db/shared/00-bootstrap/` (roles/grants), `deploy/clickhouse/users/`,
`internal/contexts/*/reports/`, `internal/reporting/`, `cmd/api`,
`docs/scenarios/*` (frescor e perfil por cenário), `docs/evaluation/`.

## Como validar

```bash
make bench                                  # p50/p95/p99 por endpoint
scripts/checks/grants.sh                    # dh_app sem acesso a dh_landing
```
```sql
SHOW GRANTS FOR dh_app;
SELECT type, query_duration_ms, user FROM system.query_log
WHERE user='dh_app' AND type='QueryFinish' AND event_time > now()-INTERVAL 1 HOUR
ORDER BY query_duration_ms DESC LIMIT 10;
```
