# ADR-0006 — Fixar convenções de Avro, subjects e compatibilidade no Schema Registry

- **Status:** Accepted
- **Data:** 2026-09-13
- **Decisores:** Engenharia de Dados
- **ADRs relacionados:** 0002, 0003

## Contexto

O Datasphere publica Avro no Schema Registry do Confluent. O connector usa
`AvroConverter`, portanto o wire format é o do Confluent: `magic byte 0x00` +
`schema id` (4 bytes big-endian) + payload Avro. Sem convenção fixa, cada tópico
evolui de um jeito e a ingestão quebra em produção, não em CI.

Além disso, a PoC **não tem o SAP**: o `cmd/producer` precisa produzir Avro
idêntico ao que o Datasphere produziria, senão a validação não vale nada.

## Decisão

1. **Wire format:** Confluent (`AvroConverter` no connector,
   `value.converter.schema.registry.url` configurado). Nada de Avro "puro" com
   schema embutido por mensagem.
2. **Estratégia de subject:** `TopicNameStrategy` — subjects
   `<topic>-key` e `<topic>-value`.
3. **Nome do tópico:** `sap.<contexto>.<entidade>.v<n>`. O `v<n>` é **versão do
   contrato**, não versão do schema. Mudança incompatível cria `v<n+1>` e um
   contrato novo; não se quebra o subject existente.
4. **Compatibilidade:** `BACKWARD` por subject (default do Confluent), afirmada
   explicitamente por `dhctl schemas apply`. Mudança que exigiria
   `FORWARD`/`NONE` é tópico novo.
5. **Regras de schema:**
   - Namespace: `br.com.gruposbf.datahouse.<contexto>`.
   - Nome do record: `PascalCase` da entidade (`Order`, `OrderItem`).
   - Todo campo tem `doc` (vem do `doc` do contrato). `dhctl contract validate`
     falha sem `doc` em campo de chave ou de relação.
   - Nullable é `["null", T]` **com `default: null`** — obrigatório para
     compatibilidade `BACKWARD`.
   - `decimal` como `bytes` com `logicalType: decimal` + `precision`/`scale`
     (nunca `double` para dinheiro).
   - Tempo: `long` com `logicalType: timestamp-micros` (`timestamp-millis` só se o
     contrato declarar).
   - Data: `int` com `logicalType: date`.
   - **Sem `union` de mais de dois ramos** e **sem referência externa de schema**:
     o ClickPipes não suporta referência Avro externa, e a PoC deve permanecer
     compatível com o destino (ADR-0008).
6. **Envelope CDC:** o payload é **flat** (already-flattened *after image*) com
   as colunas de controle no próprio record: `op`, `cdc_seq`, `source_ts`. Se a
   origem real entregar envelope Debezium-like (`before`/`after`/`op`), a SMT
   `flatten` do Connect resolve, e o contrato declara
   `source.envelope: debezium`. O default do projeto é `flat`.
7. **Chave Kafka:** record Avro com os campos de `source.key`. A chave é
   materializada em coluna pela SMT `KeyToValue` (ADR-0002) — não confiar em
   `_key` string.

## Alternativas consideradas

- **`RecordNameStrategy`/`TopicRecordNameStrategy`:** permite múltiplos tipos por
  tópico. Não precisamos, e complica o mapeamento 1 tópico→1 tabela do ADR-0002.
- **JSON Schema ou Protobuf:** a origem é Avro. Fora de escopo.
- **JSON sem schema:** perde tipagem e evolução; inviabiliza `decimal` confiável.

## Consequências

### Positivas
- Evolução de schema previsível e verificável em CI.
- `producer` da PoC gera bytes indistinguíveis da origem real.
- Compatível com ClickPipes (sem referência externa), preservando a rota de
  migração.

### Negativas / custo aceito
- `BACKWARD` proíbe remover campo e adicionar campo obrigatório. Aceito: força
  disciplina e é o default do Confluent.
- Campo novo obrigatório vira `v<n+1>` do tópico, com convivência das duas
  versões por um período. Procedimento em `docs/runbooks/add-new-topic.md`.

## Impacto no repositório

`internal/codegen/avro/`, `internal/platform/schemaregistry/`,
`schemas/avro/**` (gerado), `cmd/dhctl` (`schemas apply`), `cmd/producer`.

## Como validar

```bash
dhctl schemas apply --dry-run          # lista subjects e compatibilidade
curl -s localhost:8081/subjects | jq
curl -s localhost:8081/config/sap.sales.order.v1-value | jq '.compatibilityLevel'
# esperado: BACKWARD
dhctl schemas check-compat contracts/domains/sales/order.yaml   # testa antes de publicar
```
