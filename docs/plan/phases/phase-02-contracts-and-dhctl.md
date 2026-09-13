# Fase 02 — Contratos, codegen e migrations

- **Branch:** `feat/phase-02-contracts-and-dhctl`
- **Pré-requisito:** Fase 01 `DONE` (com o gate de paridade verde)
- **Bloqueia:** Fase 03
- **ADRs relevantes:** [0003](../../adr/0003-extensibilidade-contract-first.md), [0006](../../adr/0006-convencoes-avro-e-schema-registry.md), [0007](../../adr/0007-migrations-sql-versionado.md), [0009](../../adr/0009-layout-de-pastas-context-first.md)
- **Entrega verificável:** `make generate` é idempotente e `make migrate` cria os quatro databases do zero

## Objetivo da fase

Construir o motor de extensibilidade. Ao fim desta fase, os 8 contratos que já
existem em `contracts/domains/` produzem schemas Avro, DDL de landing e configs de
connector **gerados**, e o runner de migrations aplica SQL na ordem canônica.

Esta é a fase que paga os 30 tópicos. É também a mais densa: leia
`docs/architecture/extensibility.md` inteiro antes de começar.

---

## P02-T01 — Parse e validação de contratos

**Objetivo:** `dhctl contract validate` recusa contrato inválido com mensagem útil.

**Arquivos:**
- `internal/contract/contract.go` (structs)
- `internal/contract/parse.go`
- `internal/contract/validate.go`
- `internal/contract/validate_test.go`
- `internal/contract/testdata/` (contratos válidos e inválidos)
- `cmd/dhctl/contract.go`

**Especificação:**
- Structs Go espelhando `contracts/schema.json`. Use tags YAML.
- Validação em duas etapas: (1) contra o JSON Schema
  (`contracts/schema.json`, embutido com `go:embed`), (2) regras semânticas que o
  JSON Schema não expressa:

  | Regra | Mensagem de erro deve dizer |
  |---|---|
  | `metadata.context`/`entity` batem com o caminho do arquivo | caminho esperado |
  | `source.topic` == `sap.<context>.<entity>.v<n>` | tópico esperado |
  | `source.key` ⊆ nomes de campos de `schema.fields` | campo inexistente |
  | `source.version_column` existe em `schema.fields` | campo inexistente |
  | `source.operation_column` existe em `schema.fields` | campo inexistente |
  | `landing.order_by` **termina** em `_cdc_seq` | o porquê (L0 guarda versões) |
  | `core.order_by` **não contém** `_cdc_seq` | o porquê (senão nada deduplica) |
  | `core.order_by` ⊆ campos, e é igual a `source.key` salvo justificativa | diferença |
  | `landing.ttl_days >= 90` | o porquê (reprocesso) |
  | `decimal` tem `precision` e `scale` | qual campo |
  | `array`/`map` têm `items`; `enum` tem `values` | qual campo |
  | campo em `source.key` ou em `relations` tem `doc` | qual campo |
  | `relations[].to` aponta para contrato existente | caminho procurado |
  | `relations[].join_on.local`/`remote` existem nos respectivos contratos | qual coluna |
  | `expose_as_dictionary.attributes` ⊆ campos | qual coluna |
  | dinheiro (`precision >= 10`) não é `float64` | qual campo |

- **Erro acumulativo:** valida tudo e reporta **todos** os problemas de uma vez,
  com `arquivo:campo: mensagem`. Parar no primeiro erro faz o usuário rodar 8 vezes.
- `dhctl contract validate [paths...]` — sem argumento, valida
  `contracts/domains/**/*.yaml`. Exit 1 com qualquer erro.
- `dhctl contract list` imprime tabela: contexto, entidade, tópico, chave,
  `serving`, `analytics`.
- `internal/contract/testdata/` tem, no mínimo, um contrato inválido por regra da
  tabela acima, e o teste garante que a mensagem correta aparece.

**Critérios de aceite:**
- [ ] `dhctl contract validate` passa nos 8 contratos existentes
- [ ] cada regra da tabela tem um caso em `testdata/` que falha com a mensagem certa
- [ ] `go test ./internal/contract/...` passa
- [ ] um contrato com 3 erros reporta os 3, não só o primeiro
- [ ] `dhctl contract list` lista as 8 entidades

**Validação:** `make build && ./bin/dhctl contract validate && go test ./internal/contract/...`

**Commit:** `feat(contracts): adicionar parse e validação de descritores`

**Docs a atualizar:** `PROGRESS.md`

---

## P02-T02 — Gerador de schemas Avro

**Objetivo:** `.avsc` de chave e valor gerados a partir do contrato, conforme ADR-0006.

**Arquivos:**
- `internal/codegen/avro/avro.go`
- `internal/codegen/avro/types.go`
- `internal/codegen/avro/avro_test.go`
- `internal/codegen/avro/testdata/golden/`
- `schemas/avro/<ctx>/<entidade>-{value,key}.avsc` (gerados)

**Especificação:**
- Namespace: `<schema.namespace>.<context>` (default
  `br.com.gruposbf.datahouse.<context>`). Record em `PascalCase` da entidade.
- Mapeamento de tipos **exatamente** conforme a tabela de
  `docs/architecture/extensibility.md`. Tipo fora dela → erro, nunca degradar
  para `string`.
- `nullable: true` → `["null", T]` **com `default: null`** (obrigatório para
  `BACKWARD`).
- `decimal` → `bytes` com `logicalType: decimal`, `precision`, `scale`.
- `timestamp_micros` → `long` + `logicalType: timestamp-micros`.
- `date` → `int` + `logicalType: date`.
- Todo campo carrega `doc` quando o contrato tiver.
- Schema da **chave**: record com apenas os campos de `source.key`, nome
  `<Entity>Key`.
- **Sem referência externa de schema** e sem union de mais de 2 ramos (ADR-0006 §5)
  — o gerador não tem como produzi-los, e isso é intencional (compatibilidade com
  ClickPipes, ADR-0008).
- Saída com chaves **ordenadas deterministicamente** e indentação fixa: rodar duas
  vezes produz bytes idênticos.
- Header não é possível em JSON puro; a marca de geração vai no campo
  `doc` do record: `GENERATED by dhctl from contracts/domains/<ctx>/<e>.yaml (contract-hash: <12 chars>)`.
- Testes *golden*: `testdata/golden/` com a saída esperada para os 8 contratos.

**Critérios de aceite:**
- [ ] 16 arquivos gerados (8 `-value.avsc` + 8 `-key.avsc`)
- [ ] todo `.avsc` é JSON válido e passa por um parser Avro (use uma lib Go de Avro
      para carregar cada schema no teste)
- [ ] todo campo `nullable` tem `["null", T]` **e** `default: null`
- [ ] nenhum campo monetário é `double`
- [ ] rodar o gerador duas vezes não muda nenhum byte
- [ ] os *golden tests* passam

**Validação:** `./bin/dhctl generate --only=avro && git diff --exit-code schemas/ && go test ./internal/codegen/avro/...`

**Commit:** `feat(codegen): gerar schemas Avro a partir dos contratos`

**Docs a atualizar:** `PROGRESS.md`

---

## P02-T03 — Gerador de DDL de landing

**Objetivo:** tabela L0 por entidade, com as colunas técnicas injetadas.

**Arquivos:**
- `internal/codegen/ddl/landing.go`
- `internal/codegen/ddl/landing.sql.tmpl`
- `internal/codegen/ddl/types.go`
- `internal/codegen/ddl/landing_test.go` + `testdata/golden/`
- `internal/contexts/<ctx>/sql/10-landing/<seq>__<entidade>_raw.sql` (gerados)

**Especificação:**
- Nome: `dh_landing.<context>__<entity>_raw`.
- Colunas do contrato, tipadas conforme a tabela de tipos; `low_cardinality: true`
  → `LowCardinality(String)`.
- **Colunas técnicas injetadas em toda tabela L0**, nesta ordem, ao final:
  ```
  _topic       LowCardinality(String),
  _partition   UInt16,
  _offset      UInt64,
  _kafka_ts    DateTime64(3),
  _ingested_at DateTime64(3) DEFAULT now64(3),
  _op          Enum8('c'=1,'u'=2,'d'=3,'r'=4),
  _cdc_seq     UInt64,
  _schema_id   UInt32
  ```
- `ENGINE = MergeTree`, `PARTITION BY` e `ORDER BY` do contrato,
  `TTL toDateTime(_kafka_ts) + INTERVAL <ttl_days> DAY`.
- Compressão: `CODEC(ZSTD(1))` em colunas `String` largas; o default do servidor
  nos demais. Justifique a escolha em comentário no template.
- `CREATE TABLE IF NOT EXISTS ... {ON_CLUSTER}` (ADR-0007, ADR-0008).
- Header obrigatório:
  ```
  -- GENERATED by dhctl from contracts/domains/<ctx>/<e>.yaml — DO NOT EDIT
  -- contract-hash: <12 chars>
  ```
- `<seq>` derivado de forma estável do nome da entidade (ex.: ordem alfabética
  dentro do contexto, passo 10), para que adicionar entidade não renumere as outras.

**Critérios de aceite:**
- [ ] 8 arquivos gerados, um por entidade
- [ ] todo arquivo tem o header e o `contract-hash`
- [ ] todo arquivo contém `{ON_CLUSTER}` e `IF NOT EXISTS`
- [ ] as 8 colunas técnicas estão presentes em todos, na ordem especificada
- [ ] `landing.order_by` do contrato aparece literalmente no `ORDER BY`
- [ ] gerar duas vezes não muda byte
- [ ] *golden tests* passam

**Validação:** `./bin/dhctl generate --only=ddl && git diff --exit-code internal/contexts/*/sql/10-landing/`

**Commit:** `feat(codegen): gerar DDL de landing a partir dos contratos`

**Docs a atualizar:** `PROGRESS.md`

---

## P02-T04 — Gerador de config de connector e de tópicos

**Objetivo:** um `.json` de connector e uma entrada de tópico por entidade.

**Arquivos:**
- `internal/codegen/connector/connector.go` + `.tmpl`
- `internal/codegen/connector/topics.go`
- `internal/codegen/connector/connector_test.go` + `testdata/golden/`
- `deploy/connect/connectors/<topic>.json` (gerados)
- `deploy/kafka/topics.yaml` (gerado)

**Especificação da config do connector** (ADR-0002):
```json
{
  "name": "sink-sap-sales-order-v1",
  "config": {
    "connector.class": "com.clickhouse.kafka.connect.ClickHouseSinkConnector",
    "tasks.max": "<partitions do contrato>",
    "topics": "sap.sales.order.v1",
    "hostname": "${env:CLICKHOUSE_HOST}",
    "port": "8123",
    "database": "dh_landing",
    "username": "${env:CLICKHOUSE_PIPELINE_USER}",
    "password": "${env:CLICKHOUSE_PIPELINE_PASSWORD}",
    "ssl": "false",
    "exactlyOnce": "true",
    "topic2TableMap": "sap.sales.order.v1=sales__order_raw",
    "key.converter": "io.confluent.connect.avro.AvroConverter",
    "key.converter.schema.registry.url": "${env:SCHEMA_REGISTRY_URL}",
    "value.converter": "io.confluent.connect.avro.AvroConverter",
    "value.converter.schema.registry.url": "${env:SCHEMA_REGISTRY_URL}",
    "errors.tolerance": "all",
    "errors.deadletterqueue.topic.name": "dlq.sap.sales.order.v1",
    "errors.deadletterqueue.context.headers.enable": "true",
    "errors.deadletterqueue.topic.replication.factor": "1",
    "errors.log.enable": "true",
    "errors.log.include.messages": "true",
    "consumer.override.max.poll.records": "10000",
    "transforms": "keyToValue",
    "transforms.keyToValue.type": "com.clickhouse.kafka.connect.transforms.KeyToValue"
  }
}
```
Regras:
- **Nenhum segredo literal.** Sempre `${env:...}`; os valores vêm de `.env`.
- `exactlyOnce: true` e **nenhuma** configuração de buffering interno — os dois são
  incompatíveis (ADR-0002).
- `topic2TableMap` sempre 1:1.
- `transforms`: só `KeyToValue`; adicionar `flatten` **apenas** se
  `source.envelope == debezium` no contrato.
- DLQ obrigatória, nome `dlq.<topic>`.
- Header de geração como comentário não existe em JSON: inclua
  `"_generated_from"` e `"_contract_hash"` **fora** do objeto `config`, no nível
  raiz, e o aplicador de connector os remove antes do POST.

**Especificação de `topics.yaml`:** lista com `name`, `partitions`,
`replication_factor: 1`, `configs` (`cleanup.policy: delete`,
`retention.ms` coerente com `landing.ttl_days`), mais um tópico `dlq.<topic>` por
entidade (1 partição).

**Critérios de aceite:**
- [ ] 8 `.json` gerados em `deploy/connect/connectors/`
- [ ] nenhum contém senha, host ou URL literal (`grep` por `://` e por `password":"[^$]`)
- [ ] todos com `exactlyOnce=true` e `topic2TableMap` 1:1
- [ ] todos com `errors.deadletterqueue.topic.name` preenchido
- [ ] `topics.yaml` tem 16 tópicos (8 + 8 DLQ) com as partições dos contratos
- [ ] gerar duas vezes não muda byte
- [ ] `jq .` valida todos os `.json`

**Validação:**
```bash
./bin/dhctl generate --only=connector && git diff --exit-code deploy/
for f in deploy/connect/connectors/*.json; do jq -e '.config.exactlyOnce=="true"' "$f" >/dev/null; done
! grep -rlE '"(password|hostname)"\s*:\s*"[^$]' deploy/connect/connectors/
```

**Commit:** `feat(codegen): gerar config de connector e definição de tópicos`

**Docs a atualizar:** `PROGRESS.md`

---

## P02-T05 — `dhctl generate` com `--check` e esqueleto de core

**Objetivo:** um comando gera tudo, e `--check` transforma divergência em erro de build.

**Arquivos:**
- `cmd/dhctl/generate.go`
- `internal/codegen/generate.go`
- `internal/codegen/ddl/core.sql.tmpl`
- `scripts/checks/generated-files-clean.sh`
- `scripts/checks/contract-ttl.sh`
- `internal/contexts/<ctx>/sql/20-core/<seq>__<entidade>.sql.tmpl` (esqueletos gerados)

**Especificação:**
- `dhctl generate [--only=avro|ddl|connector|core] [--check]`.
- `--check` **não escreve**: compara o que seria gerado com o que está em disco e
  sai 1 listando os arquivos divergentes e o `contract-hash` esperado.
- **Esqueleto de core** (`.sql.tmpl`), gerado **só se o `.sql` correspondente não
  existir** — nunca sobrescreve trabalho humano. Conteúdo: a tabela core, a MV
  incremental L0→L1 e a view `v_*_current`, com `-- TODO:` nos pontos que exigem
  decisão (tipos derivados, partição). Header:
  `-- SKELETON generated by dhctl — revise, renomeie para .sql e remova esta linha`.
- `generated-files-clean.sh` = `dhctl generate --check`.
- `contract-ttl.sh` falha se algum contrato tiver `landing.ttl_days < 90`.
- Ambos entram em `make verify`.

**Critérios de aceite:**
- [ ] `make generate` produz avsc + DDL de landing + connectors + esqueletos
- [ ] `make generate && git diff --exit-code` — nada muda na segunda execução
- [ ] `dhctl generate --check` sai 0 com o repositório limpo
- [ ] editar um `.avsc` gerado à mão faz `make verify` **falhar** nomeando o arquivo
- [ ] `generate` **não** sobrescreve um `20-core/*.sql` existente
- [ ] `contract-ttl.sh` falha ao baixar um `ttl_days` para 30 (desfazer)

**Validação:** `make generate && git diff --exit-code && make verify`

**Commit:** `feat(codegen): adicionar dhctl generate com modo --check`

**Docs a atualizar:** `PROGRESS.md`; `scripts/checks/README.md`

---

## P02-T06 — Runner de migrations

**Objetivo:** `make migrate` cria o banco do zero, na ordem canônica, idempotente.

**Arquivos:**
- `internal/migrate/migrate.go`
- `internal/migrate/discover.go`
- `internal/migrate/migrate_test.go`
- `internal/platform/clickhouseclient/client.go`
- `cmd/dhctl/migrate.go`
- `db/shared/00-bootstrap/0010__databases.sql`
- `db/shared/00-bootstrap/0020__roles.sql`
- `db/shared/00-bootstrap/0030__dh_meta.sql`
- `deploy/clickhouse/users/dh_users.xml`

**Especificação:**
- **Ordem canônica** (ADR-0007): `db/shared/00-bootstrap/` → para cada camada
  `10-landing`, `20-core`, `30-marts`, `40-reports`, todos os contextos em ordem
  alfabética **com `analytics` por último**, e dentro do contexto por nome de
  arquivo.
- **ID** da migration = caminho relativo do arquivo.
- Registro em `dh_meta.schema_migrations(id String, checksum String, applied_at DateTime64(3), duration_ms UInt32)`,
  `ENGINE = ReplacingMergeTree(applied_at) ORDER BY (id)`.
- **Checksum** SHA-256 do conteúdo. Arquivo já aplicado com checksum diferente →
  **falha** mostrando o ID e os dois checksums, e orientando a criar migration
  nova. `--allow-drift` só com aviso em vermelho.
- Substituição de `{ON_CLUSTER}` por `ON CLUSTER '<nome>'` ou string vazia,
  conforme `CLICKHOUSE_CLUSTER` (vazio = single-node).
- Múltiplos statements por arquivo, separados por `;`, executados na ordem, na
  mesma conexão.
- `dhctl migrate` aplica pendentes; `dhctl migrate --status` lista
  `ID | aplicada | checksum | duração`; `dhctl migrate --dry-run` só lista a ordem.
- `0010__databases.sql`: `dh_landing`, `dh_core`, `dh_marts`, `dh_reports`, `dh_meta`.
- `0020__roles.sql`: roles `dh_app`, `dh_analyst`, `dh_pipeline` com os grants do
  ADR-0011 §3 — **`dh_app` sem nenhum grant em `dh_landing`**; em `dh_core`, só nas
  views `v_*`.
- `0030__dh_meta.sql`: `schema_migrations`, `dq_checks`, `dq_check_results`,
  `pipeline_health` (esquemas conforme ADR-0010).
- `deploy/clickhouse/users/dh_users.xml`: perfis com os settings do ADR-0011 §4
  (`dh_app`: `max_execution_time=3`, `readonly=1`, `final=1`;
  `dh_analyst`: `max_execution_time=60`; `dh_pipeline`: DDL e INSERT).
- `make reset` dropa `dh_*` e reaplica; permitido porque L0 é reconstruível.

**Critérios de aceite:**
- [ ] `make reset && make migrate` cria os 5 databases e as 8 tabelas de landing
- [ ] `make migrate` de novo aplica **0** migrations
- [ ] `dhctl migrate --status` mostra a ordem canônica com `analytics` por último
- [ ] alterar uma migration aplicada faz `dhctl migrate` falhar com os dois checksums
- [ ] `SHOW GRANTS FOR dh_app` não menciona `dh_landing`
- [ ] `go test ./internal/migrate/...` passa (incluindo teste de ordenação)
- [ ] com `CLICKHOUSE_CLUSTER` vazio, nenhum DDL executado contém `ON CLUSTER`

**Validação:**
```bash
make reset && make migrate && make migrate && ./bin/dhctl migrate --status
```

**Commit:** `feat(migrate): adicionar runner de migrations com ordenação por camada`

**Docs a atualizar:** `PROGRESS.md`; `db/CLAUDE.md`

---

## P02-T07 — Checks de SQL e runbook de novo tópico

**Objetivo:** fechar a fase com os checks que protegem as regras do ADR-0009 e o
runbook que prova a extensibilidade.

**Arquivos:**
- `scripts/checks/no-cross-context-sql.sh`
- `scripts/checks/parity.sh` (estender com as regras de DDL)
- `docs/runbooks/add-new-topic.md`

**Especificação:**
- `no-cross-context-sql.sh`: para cada arquivo em
  `internal/contexts/<ctx>/sql/`, extrair as referências `dh_<db>.<ctx2>__<...>` e
  falhar se `<ctx2> != <ctx>`, **exceto** no contexto `analytics` e exceto
  referências a `dh_core.dict__*` (dictionaries são compartilhados por desenho).
- `parity.sh`: acrescentar — nenhum DDL com engine de arquivo local; todo `.sql`
  fora de `40-reports/` contém `{ON_CLUSTER}`; nenhum `ALTER TABLE ... DELETE`
  ou `... UPDATE`.
- `docs/runbooks/add-new-topic.md`: procedimento completo em passos numerados
  (escrever contrato → validar → `make generate` → completar `20-core` → `make
  bootstrap` → `dhctl dq run`), com a seção **"Decisões que você tem de tomar"**
  (`order_by` de landing e de core, `ttl_days`, `version_column`, `relations`,
  `expose_as_dictionary`, `freshness_sla_minutes`) e a seção **"Erros comuns"**
  (`_cdc_seq` no `core.order_by`; esquecer `_cdc_seq` no `landing.order_by`; chave
  `on` em vez de `join_on`; vírgula sem aspas em `doc:` dentro de flow mapping;
  `ttl_days` abaixo de 90; tipo fora da tabela de mapeamento). Inclua também o
  procedimento de **mudança incompatível de schema** (criar `v<n+1>`, conviver com
  as duas versões, desativar a antiga).

**Critérios de aceite:**
- [ ] `no-cross-context-sql.sh` sai 0 no repositório atual
- [ ] sai 1 se um arquivo de `contexts/sales/sql/` referenciar `dh_core.customer__*`
      (teste e desfazer)
- [ ] `parity.sh` falha se um `.sql` de `20-core/` perder o `{ON_CLUSTER}`
- [ ] `make verify` roda os dois
- [ ] o runbook cobre os 6 erros comuns listados

**Validação:** `make verify`

**Commit:** `test(codegen): adicionar checks de SQL por contexto e runbook de novo tópico`

**Docs a atualizar:** `PROGRESS.md`; `scripts/checks/README.md`

---

## Critérios de aceite da fase

- [ ] `dhctl contract validate` passa nos 8 contratos
- [ ] `make generate` idempotente (`git diff --exit-code` limpo na 2ª execução)
- [ ] `dhctl generate --check` sai 0; editar arquivo gerado faz `make verify` falhar
- [ ] `make reset && make migrate` cria 5 databases e 8 tabelas de landing do zero
- [ ] `make migrate` repetido aplica 0 migrations
- [ ] `dh_app` sem grant em `dh_landing`
- [ ] `make verify` passa com todos os checks novos
- [ ] `docs/runbooks/add-new-topic.md` escrito
- [ ] `PROGRESS.md` com a Fase 02 `DONE`
