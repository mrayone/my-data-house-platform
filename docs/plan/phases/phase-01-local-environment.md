# Fase 01 — Ambiente local e verificação de paridade com o Cloud

- **Branch:** `feat/phase-01-local-environment`
- **Pré-requisito:** Fase 00 `DONE`
- **Bloqueia:** Fase 02
- **ADRs relevantes:** [0002](../../adr/0002-ingestao-via-clickhouse-kafka-connect-sink.md), [0008](../../adr/0008-topologia-self-hosted-e-paridade-com-clickhouse-cloud.md)
- **Entrega verificável:** `make up` sobe tudo saudável e `make parity-check` confirma os dois pré-requisitos do ClickHouse Cloud

## Objetivo da fase

Ambiente completo em Docker Compose e — o mais importante — **confirmar antes de
construir sobre eles** que Refreshable Materialized View e KeeperMap estão
disponíveis. Se um dos dois falhar, toda a arquitetura de marts ou de exactly-once
precisa de ADR novo, e é infinitamente mais barato descobrir agora.

> **Atenção, executor:** a tarefa P01-T04 é um **gate**. Se ela falhar, **pare a
> fase**, registre em `PROGRESS.md` → Bloqueios, e escale. Não continue para a
> Fase 02 com o gate vermelho.

---

## P01-T01 — ClickHouse e Keeper

**Objetivo:** ClickHouse single-node com Keeper, pronto para KeeperMap.

**Arquivos:**
- `docker-compose.yml` (serviços `clickhouse`, `clickhouse-keeper`)
- `deploy/clickhouse/config/00-logging.xml`
- `deploy/clickhouse/config/10-keeper.xml`
- `deploy/clickhouse/config/20-keeper-map.xml`
- `deploy/clickhouse/config/30-features.xml`
- `deploy/keeper/keeper_config.xml`

**Especificação:**
- Imagem `clickhouse/clickhouse-server` **fixada por tag e digest** (ADR-0008 §6).
  Registre a versão escolhida em `docs/evaluation/clickhouse-cloud-gcp-criteria.md`.
- Portas: `8123` (HTTP), `9000` (nativo). Keeper em `9181`.
- `10-keeper.xml`: `<zookeeper>` apontando para o serviço `clickhouse-keeper`.
- `20-keeper-map.xml`: `<keeper_map_path_prefix>/keeper_map_tables</keeper_map_path_prefix>`
  — **requisito do exactly-once do connector** (ADR-0002).
- `30-features.xml`: habilita o que a versão exigir para Refreshable MV
  (`allow_experimental_refreshable_materialized_view` quando aplicável). Se a
  versão escolhida já traz a feature estável, **deixe o arquivo com um comentário
  dizendo isso** em vez de removê-lo — a rastreabilidade importa para a avaliação.
- `healthcheck` real em ambos: ClickHouse por `SELECT 1` via HTTP; Keeper por
  `ruok`.
- Volumes nomeados para dados; `reset-env` os remove.
- **Sem** engine de arquivo local, sem disco nomeado exótico (ADR-0008 §1).

**Critérios de aceite:**
- [ ] `docker compose up -d clickhouse clickhouse-keeper` deixa os dois `healthy`
- [ ] `SELECT version()` responde e a versão bate com a registrada no doc de avaliação
- [ ] `SELECT * FROM system.zookeeper WHERE path='/'` responde (Keeper conectado)
- [ ] `SELECT value FROM system.server_settings WHERE name='keeper_map_path_prefix'`
      retorna o prefixo configurado

**Validação:** `make up && make ps && make parity-check` (o `parity-check` completo vem em P01-T04)

**Commit:** `build(deploy): adicionar ClickHouse e Keeper ao ambiente local`

**Docs a atualizar:** `PROGRESS.md`; `docs/evaluation/clickhouse-cloud-gcp-criteria.md` (versão fixada)

---

## P01-T02 — Kafka e Schema Registry

**Objetivo:** Kafka em KRaft com Schema Registry compatível com Confluent.

**Arquivos:** `docker-compose.yml` (serviços `kafka`, `schema-registry`), `deploy/kafka/README.md`

**Especificação:**
- Kafka em **KRaft** (sem ZooKeeper), imagem Confluent fixada por tag.
- Listener interno para os outros containers e externo em `localhost:9092` para o
  `producer` e o `dhctl` rodando na máquina.
- Schema Registry em `8081`, com `SCHEMA_REGISTRY_SCHEMA_COMPATIBILITY_LEVEL=backward`
  (ADR-0006 §4).
- `auto.create.topics.enable=false` — tópico é criado por `dhctl topics apply`,
  com partições declaradas no contrato. Criação automática esconderia contrato
  faltante.
- `healthcheck` em ambos.
- `deploy/kafka/README.md`: por que KRaft, por que `auto.create` desligado, e como
  o `topics.yaml` é gerado (ADR-0003).

**Critérios de aceite:**
- [ ] os dois serviços `healthy`
- [ ] `curl -s localhost:8081/subjects` retorna `[]`
- [ ] `curl -s localhost:8081/config | jq -r .compatibilityLevel` retorna `BACKWARD`
- [ ] produzir em tópico inexistente **falha** (auto-create desligado)

**Validação:** `make up && curl -sf localhost:8081/config`

**Commit:** `build(deploy): adicionar Kafka (KRaft) e Schema Registry`

**Docs a atualizar:** `PROGRESS.md`

---

## P01-T03 — Kafka Connect com o plugin do ClickHouse

**Objetivo:** Connect rodando com o `clickhouse-kafka-connect` instalado.

**Arquivos:**
- `deploy/connect/Dockerfile`
- `docker-compose.yml` (serviço `kafka-connect`)
- `deploy/connect/README.md`

**Especificação:**
- Imagem base `confluentinc/cp-kafka-connect` fixada por tag.
- O `Dockerfile` instala o **connector oficial do ClickHouse**, com versão
  **pinada** (via `confluent-hub install` ou download do release do GitHub
  `ClickHouse/clickhouse-kafka-connect`). Registre a versão no README e no doc de
  avaliação.
- Requisitos do connector (ADR-0002): ClickHouse ≥ 23.3, Kafka Connect ≥ 2.7,
  Java 11+. A imagem base já cobre os dois últimos; confira.
- Converters disponíveis: `io.confluent.connect.avro.AvroConverter`.
- REST em `8083`. `healthcheck` por `GET /connectors`.
- `CONNECT_PLUGIN_PATH` incluindo o diretório do plugin.
- Distributed mode, com tópicos internos de config/offset/status criados
  explicitamente (não por auto-create).
- `deploy/connect/README.md`: versão do plugin, por que Connect e não Kafka
  Engine (aponte para o ADR-0002), e a lista de connectors que `dhctl` gerencia.

**Critérios de aceite:**
- [ ] serviço `healthy`
- [ ] `curl -s localhost:8083/connector-plugins | jq -r '.[].class' | grep -i clickhouse`
      encontra `com.clickhouse.kafka.connect.ClickHouseSinkConnector`
- [ ] `curl -s localhost:8083/connectors` retorna `[]`
- [ ] a versão do plugin está registrada no README e no doc de avaliação

**Validação:** `make up && make connectors-status` (o alvo completo vem na Fase 04; aqui basta listar plugins)

**Commit:** `build(deploy): adicionar Kafka Connect com o plugin do ClickHouse`

**Docs a atualizar:** `PROGRESS.md`; `deploy/connect/README.md`

---

## P01-T04 — **GATE** — verificação de paridade com o ClickHouse Cloud

**Objetivo:** provar, **antes** de construir sobre eles, que os dois pilares da
arquitetura funcionam nesta versão — e documentar a pergunta aberta sobre o Cloud.

**Arquivos:**
- `scripts/checks/parity.sh`
- `test/e2e/parity_test.go` (ou script SQL equivalente chamado pelo `parity.sh`)
- `docs/evaluation/clickhouse-cloud-gcp-criteria.md` (preencher a seção de pré-requisitos)

**Especificação:**

`make parity-check` executa, contra o ClickHouse local, e falha se algo não passar:

1. **Refreshable MV funciona.** Criar tabela fonte, tabela destino e
   `CREATE MATERIALIZED VIEW ... REFRESH EVERY 1 MINUTE APPEND TO ...`; inserir
   dado; aguardar o refresh; confirmar que o destino foi populado e que
   `system.view_refreshes` mostra `last_refresh_result = 'Finished'` sem exceção.
   **Este é o pilar da camada de marts (ADR-0005).**
2. **KeeperMap funciona.** Criar `ENGINE = KeeperMap('/test_parity')` com chave
   primária, inserir, ler e apagar a tabela.
   **Este é o pilar do exactly-once (ADR-0002).**
3. **`ASOF JOIN` funciona** e escolhe a versão correta (teste mínimo do
   cenário 005).
4. **`Dictionary` com `COMPLEX_KEY_HASHED`** carrega de uma query ClickHouse e
   `dictGet` responde.
5. **`ReplacingMergeTree(version, is_deleted)`** aceita os dois parâmetros e
   `FINAL` esconde a linha deletada (pilar do ADR-0004).
6. **Projection** pode ser adicionada e é usada (`EXPLAIN indexes = 1`).
7. **Sem engine de arquivo local** em nenhum DDL do repositório.
8. **Todo DDL versionado contém `{ON_CLUSTER}`**, exceto o de `40-reports/`
   (views não precisam).

Ao final, o script imprime uma tabela `CAPACIDADE | LOCAL | CLOUD` onde a coluna
`CLOUD` é `A VERIFICAR` — e essa tabela é copiada para
`docs/evaluation/clickhouse-cloud-gcp-criteria.md`, na seção "Pré-requisitos
bloqueantes", junto com a instrução de como verificar cada um (docs oficiais ou
trial). **A coluna `CLOUD` só sai de `A VERIFICAR` com evidência citada.**

**Critérios de aceite:**
- [ ] `make parity-check` sai 0 com os 8 itens verdes localmente
- [ ] `system.view_refreshes` mostra o refresh do teste como `Finished`
- [ ] a tabela `KeeperMap` de teste é criada, lida e removida
- [ ] `docs/evaluation/clickhouse-cloud-gcp-criteria.md` contém a tabela de
      pré-requisitos com a versão do ClickHouse e a do plugin do connector
- [ ] **se qualquer item falhar:** a fase para, o bloqueio está em `PROGRESS.md`
      com o erro literal, e nada da Fase 02 foi iniciado

**Validação:** `make parity-check`

**Commit:** `test(deploy): adicionar gate de paridade com ClickHouse Cloud`

**Docs a atualizar:** `PROGRESS.md`; `docs/evaluation/clickhouse-cloud-gcp-criteria.md`

---

## P01-T05 — Runbook do ambiente local

**Objetivo:** qualquer pessoa sobe, derruba e diagnostica o ambiente sem
perguntar.

**Arquivos:** `docs/runbooks/local-environment.md`, `Makefile` (alvos `up down logs ps reset-env parity-check`)

**Especificação:**
- Alvos: `up` (com espera por healthy), `down`, `logs` (`SERVICE=` opcional),
  `ps`, `reset-env` (down + remove volumes), `parity-check`.
- `up` **espera** os healthchecks e falha com mensagem clara em timeout — não
  retorna verde com serviço subindo.
- O runbook cobre: requisitos de máquina (RAM/CPU mínimos, e quanto o Compose
  reserva), subir/derrubar, portas e para que serve cada uma, credenciais de
  desenvolvimento (de `.env.example`), como abrir o `clickhouse-client`, e uma
  tabela **sintoma → causa provável → ação** com no mínimo: porta ocupada,
  ClickHouse sem memória, Keeper não conectado, Connect sem o plugin, Schema
  Registry recusando conexão.

**Critérios de aceite:**
- [ ] `make reset-env && make up` funciona a partir do zero
- [ ] `make up` falha com mensagem clara se uma porta estiver ocupada
- [ ] o runbook lista todas as portas expostas no `docker-compose.yml`
- [ ] um leitor que nunca viu o projeto sobe o ambiente seguindo só o runbook

**Validação:** `make reset-env && make up && make ps && make parity-check`

**Commit:** `docs(deploy): adicionar runbook do ambiente local`

**Docs a atualizar:** `PROGRESS.md`; `README.md` (link para o runbook)

---

## Critérios de aceite da fase

- [ ] `make up` deixa os 4 serviços `healthy`
- [ ] **`make parity-check` verde** — os 8 itens, com destaque para Refreshable MV
      e KeeperMap
- [ ] `docs/evaluation/clickhouse-cloud-gcp-criteria.md` com a tabela de
      pré-requisitos e as versões fixadas
- [ ] `make reset-env && make up` reproduz do zero
- [ ] `make verify` continua passando
- [ ] `PROGRESS.md` com a Fase 01 `DONE`
