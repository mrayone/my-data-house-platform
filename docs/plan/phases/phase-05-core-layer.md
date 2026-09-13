# Fase 05 — Camada core (L1): dedup, delete e fora de ordem

- **Branch:** `feat/phase-05-core-layer`
- **Pré-requisito:** Fase 04 `DONE`
- **Bloqueia:** Fase 06
- **ADRs relevantes:** [0004](../../adr/0004-semantica-cdc-dedup-ordem-delete.md), [0001](../../adr/0001-modelo-em-camadas-no-clickhouse.md)
- **Entrega verificável:** dedup, delete e chegada fora de ordem provados por e2e nas 8 entidades

## Objetivo da fase

Transformar L0 (todas as versões) em L1 (estado atual). Esta é a fase onde a
**corretude** do sistema se decide: se o dedup estiver errado aqui, todos os dez
cenários produzem números plausíveis e errados.

Leia o ADR-0004 inteiro antes de começar. Em particular, §4 (as três formas
permitidas de leitura) e §6 (a proibição de agregador incremental sobre mutável).

> **Contextos podem ser paralelizados.** As 8 entidades são independentes entre si
> nesta fase. O que não pode: começar a T04 antes de T01-T03 estarem completas para
> todas.

---

## P05-T01 — Tabelas core, MVs incrementais e views correntes

**Objetivo:** os três objetos de L1 para cada uma das 8 entidades.

**Arquivos:** `internal/contexts/<ctx>/sql/20-core/<seq>__<entidade>.sql` (8 arquivos, a partir dos esqueletos `.sql.tmpl` da Fase 02)

**Especificação:** cada arquivo contém, na ordem, três statements:

**(1) A tabela**
```sql
CREATE TABLE IF NOT EXISTS dh_core.<ctx>__<entidade> {ON_CLUSTER}
( <colunas do contrato, tipadas>,
  _cdc_seq     UInt64,
  _is_deleted  UInt8 DEFAULT 0,
  _ingested_at DateTime64(3) )
ENGINE = ReplacingMergeTree(_cdc_seq, _is_deleted)
PARTITION BY <core.partition_by do contrato>
ORDER BY (<core.order_by do contrato>);
```
- `ORDER BY` é a **chave de negócio**, **sem `_cdc_seq`**. Com `_cdc_seq` no
  `ORDER BY`, cada versão é uma linha distinta e **nada deduplica** — é o erro
  número um desta fase.
- As colunas técnicas de L0 (`_topic`, `_partition`, `_offset`, `_kafka_ts`,
  `_op`, `_schema_id`) **não** são propagadas para L1; só `_cdc_seq`,
  `_is_deleted` e `_ingested_at`.

**(2) A MV incremental**
```sql
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_core.mv__<ctx>__<entidade>_raw__to__<entidade> {ON_CLUSTER}
TO dh_core.<ctx>__<entidade> AS
SELECT <colunas>,
       _cdc_seq,
       if(_op = 'd', 1, 0) AS _is_deleted,
       _ingested_at
FROM dh_landing.<ctx>__<entidade>_raw;
```
- **Sem `JOIN`. Sem subquery em outra tabela. Sem `GROUP BY`.** Transformação de
  linha única (ADR-0005 §1).
- `soft_delete_operations` do contrato define a condição de `_is_deleted`.

**(3) A view corrente — o único ponto de leitura permitido**
```sql
CREATE VIEW IF NOT EXISTS dh_core.v_<ctx>__<entidade>_current {ON_CLUSTER} AS
SELECT * EXCEPT (_is_deleted)
FROM dh_core.<ctx>__<entidade> FINAL
WHERE _is_deleted = 0;
```

**Critérios de aceite:**
- [ ] 8 arquivos, cada um com os 3 statements
- [ ] nenhum `core.order_by` contém `_cdc_seq`
- [ ] nenhuma MV desta fase contém `JOIN`, `GROUP BY` ou subquery
- [ ] `make migrate` aplica os 8 sem erro
- [ ] `SELECT count() FROM system.tables WHERE database='dh_core'` == 24
      (8 tabelas + 8 MVs + 8 views)
- [ ] toda view `v_*_current` usa `FINAL` **e** `WHERE _is_deleted = 0`

**Validação:**
```bash
make migrate
clickhouse-client -q "SELECT name, engine FROM system.tables WHERE database='dh_core' ORDER BY name"
```

**Commit:** `feat(sales): adicionar camada core com ReplacingMergeTree e views correntes`
(um commit por contexto é aceitável; use o escopo do contexto)

**Docs a atualizar:** `PROGRESS.md`; `CLAUDE.md` do contexto

---

## P05-T02 — Rebuild de L1 a partir de L0

**Objetivo:** provar que reprocesso não depende do Kafka.

**Arquivos:** `internal/migrate/rebuild.go`, `cmd/dhctl/rebuild.go`, `Makefile` (`core-rebuild`)

**Especificação:**
- `dhctl core rebuild [--entity=<ctx>/<e>] [--all]`: para cada entidade,
  `TRUNCATE TABLE dh_core.<ctx>__<e>` seguido de
  `INSERT INTO dh_core.<ctx>__<e> SELECT <mesma projeção da MV> FROM dh_landing.<ctx>__<e>_raw`.
- A projeção é **a mesma da MV** — para não divergirem, ela é extraída do próprio
  `CREATE MATERIALIZED VIEW` (leia de `system.tables.create_table_query` e extraia
  o `SELECT`, ou gere as duas do mesmo template). **Duplicar o SQL à mão aqui é
  proibido**: é a origem clássica de divergência entre o caminho incremental e o de
  reprocesso.
- Em lotes por partição, para não estourar memória em tabela grande.
- `--dry-run` mostra o que faria e a contagem estimada.
- Pede confirmação sem `--yes`.

**Critérios de aceite:**
- [ ] `dhctl core rebuild --all` reconstrói as 8 tabelas
- [ ] a contagem de chaves distintas em `v_*_current` é **idêntica** antes e depois
- [ ] o resultado de uma query de checagem (soma de um valor) é idêntico antes e depois
- [ ] a projeção usada no rebuild é derivada da MV, não copiada
- [ ] rodar o rebuild com o connector ativo não perde mensagem que chegar durante

**Validação:**
```bash
before=$(clickhouse-client -q "SELECT count(), sum(total_amount) FROM dh_core.v_sales__order_current")
make core-rebuild
after=$(clickhouse-client -q "SELECT count(), sum(total_amount) FROM dh_core.v_sales__order_current")
[ "$before" = "$after" ]
```

**Commit:** `feat(migrate): adicionar rebuild de L1 a partir de L0`

**Docs a atualizar:** `PROGRESS.md`; `docs/runbooks/reprocess.md`

---

## P05-T03 — Checks estáticos de corretude

**Objetivo:** as três proibições do ADR-0004 e do ADR-0005 deixam de depender de
revisão humana.

**Arquivos:**
- `scripts/checks/no-direct-core-read.sh`
- `scripts/checks/no-join-in-incremental-mv.sh`
- `scripts/checks/no-incremental-agg-on-mutable.sh`
- `scripts/checks/grants.sh`
- `scripts/checks/money-no-float.sh`

**Especificação:**

| Script | Falha quando | Exceção |
|---|---|---|
| `no-direct-core-read.sh` | SQL em `30-marts/` ou `40-reports/` referencia `dh_core.<ctx>__<entidade>` sem ser `dh_core.v_*_current` ou `dh_core.dict__*` | linha marcada com `-- dq-exception:` (cenário 009) |
| `no-join-in-incremental-mv.sh` | `CREATE MATERIALIZED VIEW` contém `JOIN` **e não** contém `REFRESH` | nenhuma |
| `no-incremental-agg-on-mutable.sh` | `CREATE MATERIALIZED VIEW` sem `REFRESH` que tenha `TO` apontando para tabela `SummingMergeTree`/`AggregatingMergeTree` **e** `FROM dh_landing.` | tabela cujo contrato declare a entidade como append-only imutável (campo a criar em `extensions` se necessário — requer ADR) |
| `grants.sh` | `SHOW GRANTS FOR dh_app` menciona `dh_landing`, ou menciona `dh_core` sem ser em view `v_*` | nenhuma |
| `money-no-float.sh` | `system.columns` tem coluna `Float*` cujo nome casa `%amount%\|%price%\|%revenue%\|%capital%\|%cost%` | colunas de razão/percentual (`%_pct`, `%_rate`) |

- Todos com mensagem que diz **qual regra**, **qual arquivo/linha** e **como
  corrigir**.
- `grants.sh` e `money-no-float.sh` precisam do banco de pé; se não houver conexão,
  devem **avisar e sair 0** (`SKIP`), não falhar — senão `make verify` fica
  impossível offline. O CI roda com o banco de pé.
- Todos entram em `make verify` e em `scripts/checks/README.md`.

**Critérios de aceite:**
- [ ] os 5 scripts saem 0 no repositório atual
- [ ] cada um sai 1 no seu caso de sabotagem (5 testes manuais, documentados no
      `README.md` dos checks e desfeitos)
- [ ] `no-direct-core-read.sh` respeita o marcador `-- dq-exception:`
- [ ] `grants.sh` e `money-no-float.sh` fazem `SKIP` sem banco
- [ ] `make verify` roda os 5

**Validação:** `make verify`

**Commit:** `test(platform): adicionar checks estáticos de corretude da camada core`

**Docs a atualizar:** `PROGRESS.md`; `scripts/checks/README.md`

---

## P05-T04 — e2e da semântica CDC (a prova da fase)

**Objetivo:** provar dedup, ordem, delete e ressurreição com dado real.

**Arquivos:** `test/e2e/cdc_semantics_test.go`

**Especificação:** usando os IDs nomeados que o `producer` gera (Fase 03), com
`make seed-adverse`:

1. **Dedup por chave.** Nenhuma chave duplicada em nenhuma `v_*_current`:
   ```sql
   SELECT count() FROM (SELECT <key>, count() c FROM dh_core.v_<ctx>__<e>_current
                        GROUP BY <key> HAVING c > 1);
   -- esperado: 0, para as 8 entidades
   ```
2. **Vence a maior `_cdc_seq`, não a última a chegar.** `ORD-OOO-1` é publicado com
   v2 **antes** de v1:
   ```sql
   SELECT order_status FROM dh_core.v_sales__order_current WHERE order_id='ORD-OOO-1';
   -- esperado: o status da maior _cdc_seq
   ```
3. **Delete respeitado.** `ORD-DEL-1`:
   ```sql
   SELECT count() FROM dh_core.v_sales__order_current WHERE order_id='ORD-DEL-1';
   -- esperado: 0
   SELECT count() FROM dh_landing.sales__order_raw WHERE order_id='ORD-DEL-1' AND _op='d';
   -- esperado: >= 1  (o dado continua em L0)
   ```
4. **Ressurreição funciona.** `ORD-RES-1` (delete e depois `_cdc_seq` maior com
   `_op='u'`):
   ```sql
   SELECT count() FROM dh_core.v_sales__order_current WHERE order_id='ORD-RES-1';
   -- esperado: 1
   ```
5. **Reconciliação L0 ↔ L1.** Para cada entidade, chaves distintas em L0 ==
   chaves em `v_*_current` + chaves deletadas.
6. **Leitura sem `FINAL` de fato difere** — o teste **demonstra o problema**:
   `SELECT count() FROM dh_core.sales__order` deve ser **maior** que
   `SELECT count() FROM dh_core.v_sales__order_current` logo após a ingestão
   (antes do merge). Se forem iguais, force parts com inserts e repita. Este item
   existe para documentar, em código executável, por que a view é obrigatória.
7. **`OPTIMIZE FINAL` não muda o resultado da view.** Rodar
   `OPTIMIZE TABLE ... FINAL` e reconferir os itens 1-5: mesmos valores. Prova que
   a corretude **não depende** de o merge ter rodado.
8. **Monotonicidade de `_cdc_seq`** em L0: nenhuma chave com `_cdc_seq = 0`; e a
   maior `_cdc_seq` por chave em L0 é a que está em L1.

**Critérios de aceite:**
- [ ] os 8 itens passam para as 8 entidades onde aplicável
- [ ] o item 6 demonstra a diferença (e o teste explica no comentário por quê)
- [ ] o item 7 passa antes **e** depois do `OPTIMIZE`
- [ ] sem `sleep` fixo; poll com timeout
- [ ] falha informativa: diz qual entidade e qual chave divergiu

**Validação:** `make bootstrap && make seed-adverse && go test ./test/e2e/ -run TestCDCSemantics -v`

**Commit:** `test(sales): adicionar e2e da semântica CDC (dedup, ordem, delete, ressurreição)`

**Docs a atualizar:** `PROGRESS.md`

---

## P05-T05 — Runbook de reprocesso

**Objetivo:** os quatro cenários de reprocesso do ADR-0001 documentados e testados.

**Arquivos:** `docs/runbooks/reprocess.md`

**Especificação:** um procedimento por cenário, com comando e verificação:

| Cenário | Procedimento | Depende do Kafka? |
|---|---|---|
| Erro na MV L0→L1 | corrigir a MV em `20-core/`, migration nova, `dhctl core rebuild --entity=...` | não |
| Erro de regra num mart | corrigir o SQL, `SYSTEM REFRESH VIEW ...` (Fase 06) | não |
| Coluna nova num contrato | migration de `ALTER`, `make generate`, backfill de L0 se derivável | só se não derivável |
| Contrato errado desde o início | replay do Kafka **com `dhctl connectors reset-state`** | sim |

Mais: como estimar o tempo, como fazer por partição em tabela grande, o que
monitorar durante (`system.mutations`, `system.merges`, `system.parts`), e a
armadilha do estado do KeeperMap no replay (aponte para
`troubleshooting-ingestion.md`).

**Critérios de aceite:**
- [ ] os 4 cenários documentados com comando executável
- [ ] o cenário 1 foi **efetivamente executado** durante a fase (o `core-rebuild` da
      T02) e o runbook reflete o que aconteceu de verdade
- [ ] o runbook diz explicitamente quais cenários **não** precisam do Kafka — é o
      argumento central do ADR-0001

**Validação:** executar o cenário 1 seguindo apenas o runbook

**Commit:** `docs(migrate): adicionar runbook de reprocesso`

**Docs a atualizar:** `PROGRESS.md`

---

## Critérios de aceite da fase

- [ ] 24 objetos em `dh_core` (8 tabelas + 8 MVs + 8 views)
- [ ] nenhuma chave duplicada em nenhuma `v_*_current`
- [ ] delete, fora de ordem e ressurreição provados por e2e
- [ ] corretude independente do merge (item 7 da T04)
- [ ] reconciliação L0↔L1 fecha para as 8 entidades
- [ ] `dhctl core rebuild --all` reproduz o mesmo resultado
- [ ] os 5 checks estáticos novos em `make verify`
- [ ] `docs/runbooks/reprocess.md` escrito e testado
- [ ] `PROGRESS.md` com a Fase 05 `DONE`
