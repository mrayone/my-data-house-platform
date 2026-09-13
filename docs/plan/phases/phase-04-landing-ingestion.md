# Fase 04 — Landing (L0) e ingestão pelo Kafka Connect

- **Branch:** `feat/phase-04-landing-ingestion`
- **Pré-requisito:** Fase 03 `DONE`
- **Bloqueia:** Fase 05
- **ADRs relevantes:** [0002](../../adr/0002-ingestao-via-clickhouse-kafka-connect-sink.md), [0001](../../adr/0001-modelo-em-camadas-no-clickhouse.md)
- **Entrega verificável:** dado nas 8 tabelas de landing, sem duplicata de `(_topic,_partition,_offset)`, DLQ vazia

## Objetivo da fase

Fechar o caminho Kafka → ClickHouse com exactly-once, e provar que a ingestão é
**burra**: nenhuma transformação, nenhum join, nenhum delete. Todo o resto da PoC
depende de L0 ser completo e fiel.

---

## P04-T01 — Aplicador de connectors

**Objetivo:** `dhctl connectors apply` sobe os 8 connectors pela REST do Connect.

**Arquivos:**
- `internal/ingestion/sink.go` (porta `Sink`)
- `internal/ingestion/connect/client.go`
- `internal/ingestion/connect/apply.go`
- `cmd/dhctl/connectors.go`

**Especificação:**
- Porta `Sink` com as operações: `Apply`, `Status`, `Pause`, `Resume`, `Restart`,
  `Delete`, `ResetState`. Dois adapters previstos: `connect` (este, o padrão) e
  `goconsumer` (escape hatch, **não implementado nesta fase** — ver ADR-0002).
- `dhctl connectors apply`: lê `deploy/connect/connectors/*.json`, **resolve
  `${env:...}`** a partir do ambiente, remove os campos `_generated_from` e
  `_contract_hash`, e faz `PUT /connectors/<name>/config` (idempotente).
- Falha se alguma variável `${env:...}` não estiver definida — nomeando-a.
- `dhctl connectors status`: tabela `connector | state | tasks | último erro`.
  Exit 1 se algum não estiver `RUNNING`.
- `dhctl connectors pause|resume|restart|delete <name|--all>`.
- **Nunca logar o valor de senha**, nem em `debug`.

**Critérios de aceite:**
- [ ] `dhctl connectors apply` cria 8 connectors
- [ ] `dhctl connectors status` mostra todos `RUNNING` e sai 0
- [ ] segunda execução do apply não altera nada (idempotente)
- [ ] remover uma env var faz o apply falhar nomeando-a
- [ ] nenhuma senha aparece nos logs (`grep` no output com `LOG_LEVEL=debug`)

**Validação:** `make connectors && make connectors-status`

**Commit:** `feat(ingestion): adicionar aplicador de connectors via REST do Connect`

**Docs a atualizar:** `PROGRESS.md`

---

## P04-T02 — `make bootstrap` (ordem de subida)

**Objetivo:** um comando leva do ambiente vazio ao dado em L0.

**Arquivos:** `Makefile`, `docs/runbooks/bootstrap.md`

**Especificação:**
- `make bootstrap` = `topics` → `schemas` → `migrate` → `connectors` →
  `connectors-status`, **nesta ordem**, parando no primeiro erro.
- A ordem não é arbitrária e o runbook explica cada dependência: tópico antes de
  schema (o subject é derivado do tópico); schema antes de connector (o
  `AvroConverter` precisa resolver o `schema_id`); migrations antes de connector
  (a tabela destino tem de existir, senão a task morre).
- `make bootstrap` é **idempotente**: rodar com o ambiente já pronto não muda nada.

**Critérios de aceite:**
- [ ] `make reset-env && make up && make bootstrap` funciona do zero
- [ ] `make bootstrap` repetido não altera nada
- [ ] o runbook justifica a ordem dos 5 passos
- [ ] inverter a ordem (teste manual: connector antes de migrate) produz task
      `FAILED` — e o runbook documenta esse sintoma

**Validação:** `make reset-env && make up && make bootstrap`

**Commit:** `build: adicionar make bootstrap com ordem de subida`

**Docs a atualizar:** `PROGRESS.md`; `docs/runbooks/bootstrap.md`

---

## P04-T03 — Ingestão ponta a ponta e prova de exactly-once

**Objetivo:** provar que o dado chega completo, uma única vez.

**Arquivos:** `test/e2e/ingestion_test.go`, `test/e2e/helpers.go`

**Especificação:**

Teste e2e que: `make bootstrap`, produz `--scale=smoke --seed=42`, aguarda a
ingestão convergir (poll com timeout, não `sleep` fixo), e então verifica:

1. **Completude:** contagem de linhas por tabela de landing == mensagens produzidas
   por tópico (o producer reporta o total; compare).
2. **Exactly-once:** zero duplicatas de `(_topic, _partition, _offset)` em cada
   tabela.
3. **Fidelidade:** para os IDs nomeados, os valores em L0 são exatamente os
   produzidos — em especial `Decimal(18,4)` e `DateTime64`.
4. **Colunas técnicas preenchidas:** nenhum `_topic` vazio, `_kafka_ts` plausível,
   `_op` sempre em `('c','u','d','r')`, `_schema_id` diferente de zero,
   `_cdc_seq` diferente de zero.
5. **A chave Kafka virou coluna** (SMT `KeyToValue`): o valor da coluna de chave
   bate com a chave produzida.
6. **DLQ vazia:** todos os 8 tópicos `dlq.*` com zero mensagens.
7. **Nenhuma transformação aconteceu:** `_op='d'` está presente em L0 e a linha
   **não** foi apagada (a ingestão não aplica delete — isso é da Fase 05).
8. **Restart não duplica:** reiniciar o connector (`dhctl connectors restart`) e
   reconferir a contagem — o número não muda.

Helpers: conexão ClickHouse, espera por convergência com timeout configurável,
contagem por tópico no Kafka, leitura de profundidade de DLQ.

**Critérios de aceite:**
- [ ] os 8 itens acima passam
- [ ] o teste não usa `sleep` fixo — usa poll com timeout e mensagem clara
- [ ] o teste é executável por `make test-e2e` e por `make verify`
- [ ] o teste falha de forma informativa se a ingestão não convergir (diz qual
      tabela está atrasada e quantas linhas faltam)

**Validação:** `make bootstrap && make seed-adverse && go test ./test/e2e/ -run TestIngestion -v`

**Commit:** `test(ingestion): adicionar e2e de ingestão com prova de exactly-once`

**Docs a atualizar:** `PROGRESS.md`

---

## P04-T04 — Mensagem malformada vai para a DLQ

**Objetivo:** provar que mensagem ruim não derruba a task nem é perdida em silêncio.

**Arquivos:** `test/e2e/dlq_test.go`, `cmd/producer/main.go` (flag `--inject-malformed`)

**Especificação:**
- `--inject-malformed=N`: produz N mensagens com payload inválido para o schema
  (ex.: `schema_id` inexistente, ou payload truncado).
- Teste: injetar 3 malformadas em `sap.sales.order.v1`, mais 100 válidas, e
  verificar:
  1. as 100 válidas chegaram em L0;
  2. as 3 estão em `dlq.sap.sales.order.v1`;
  3. a task continua `RUNNING`;
  4. o header da DLQ contém o motivo do erro
     (`errors.deadletterqueue.context.headers.enable=true`).

**Critérios de aceite:**
- [ ] os 4 itens passam
- [ ] `dhctl connectors status` continua verde depois da injeção
- [ ] a mensagem de erro no header da DLQ é legível e identifica a causa

**Validação:** `go test ./test/e2e/ -run TestDLQ -v`

**Commit:** `test(ingestion): adicionar e2e de DLQ com mensagem malformada`

**Docs a atualizar:** `PROGRESS.md`

---

## P04-T05 — Runbook de troubleshooting e reset de estado do exactly-once

**Objetivo:** documentar e automatizar a armadilha operacional mais perigosa do
connector.

**Arquivos:**
- `docs/runbooks/troubleshooting-ingestion.md`
- `cmd/dhctl/connectors.go` (subcomando `reset-state`)
- `Makefile` (`ingestion-reset-state`)

**Especificação:**
- **A armadilha (ADR-0002):** com `exactlyOnce=true`, o estado do connector fica no
  KeeperMap. **Rebobinar offsets sem limpar esse estado faz o replay ser
  silenciosamente ignorado** — nada é reinserido, e nenhum erro aparece.
- `dhctl connectors reset-state <name>`: pausa o connector, localiza e remove as
  entradas correspondentes na tabela de estado do KeeperMap, e retoma. Pede
  confirmação (`--yes` para automação) e **loga o que removeu**.
- `docs/runbooks/troubleshooting-ingestion.md`, com tabela
  **sintoma → causa provável → diagnóstico → ação** cobrindo no mínimo:

  | Sintoma | Causa provável |
  |---|---|
  | task `FAILED` no start | tabela destino inexistente (migrations não aplicadas) |
  | task `FAILED` com erro de conversão | schema do subject divergente do `.avsc` gerado |
  | dado não chega, task `RUNNING`, DLQ vazia | connector pausado, ou consumer group sem partição atribuída |
  | **replay não reinsere nada** | **estado do KeeperMap** — usar `reset-state` |
  | DLQ crescendo | mensagem malformada ou schema incompatível |
  | throughput baixo | `consumer.override.max.poll.records`; `exactlyOnce` desabilita buffering |
  | duplicatas em L0 | `exactlyOnce` não ativo, ou state store inacessível |
  | `_cdc_seq` zerado | `version_column` errada no contrato |

- Cada linha traz o comando de diagnóstico (`curl` na REST do Connect, query em
  `system.*`, `dhctl connectors status`) e a ação.

**Critérios de aceite:**
- [ ] `dhctl connectors reset-state` remove o estado e permite o replay
- [ ] teste manual documentado: rebobinar offset **sem** reset ⇒ nada reinserido;
      **com** reset ⇒ reinserido
- [ ] o runbook cobre as 8 linhas da tabela
- [ ] cada linha tem comando de diagnóstico executável

**Validação:** seguir o runbook no cenário de replay e confirmar os dois comportamentos

**Commit:** `docs(ingestion): adicionar runbook de troubleshooting e reset de estado`

**Docs a atualizar:** `PROGRESS.md`; `docs/runbooks/troubleshooting-ingestion.md`

---

## Critérios de aceite da fase

- [ ] `make reset-env && make up && make bootstrap && make seed` leva dado às 8 tabelas de landing
- [ ] `dhctl connectors status` verde
- [ ] zero duplicatas de `(_topic,_partition,_offset)` em todas as tabelas
- [ ] DLQ vazia no caminho feliz; e recebendo as malformadas no teste
- [ ] colunas técnicas preenchidas em 100% das linhas
- [ ] restart de connector não duplica
- [ ] `_op='d'` presente em L0, sem linha apagada (a ingestão não transforma)
- [ ] `make verify` passa
- [ ] `PROGRESS.md` com a Fase 04 `DONE`
