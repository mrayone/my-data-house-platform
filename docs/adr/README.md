# Architecture Decision Records

Registro imutável das decisões arquiteturais da PoC.

## Regras

- Um ADR **aceito não é editado**. Decisão mudou? Novo ADR com
  `Supersedes: ADR-000X`, e o antigo recebe `Superseded by: ADR-000Y` apenas na
  linha de status.
- Status: `Proposed` → `Accepted` → `Superseded` | `Deprecated`.
- Numeração sequencial de 4 dígitos, nunca reutilizada.
- Todo ADR responde: **qual força obrigou a decisão**, **o que foi descartado e
  por quê**, e **o que essa decisão custa**.
- Use `0000-template.md` como base.

## Índice

| # | Decisão | Status | Impacta |
|---|---|---|---|
| [0001](0001-modelo-em-camadas-no-clickhouse.md) | Modelo em camadas L0→L3 dentro do ClickHouse | Accepted | modelagem, migrations |
| [0002](0002-ingestao-via-clickhouse-kafka-connect-sink.md) | Ingestão via ClickHouse Kafka Connect Sink | Accepted | ingestão, papel do Go, portabilidade Cloud |
| [0003](0003-extensibilidade-contract-first.md) | Extensibilidade contract-first com codegen | Accepted | contracts, codegen, dhctl |
| [0004](0004-semantica-cdc-dedup-ordem-delete.md) | Semântica CDC: dedup, fora de ordem e delete | Accepted | camada core, corretude |
| [0005](0005-estrategia-de-agregacao-mv-refreshable-dictionary.md) | Agregação: MV incremental × Refreshable MV × Dictionary | Accepted | marts, corretude |
| [0006](0006-convencoes-avro-e-schema-registry.md) | Convenções Avro e Schema Registry | Accepted | contratos, evolução |
| [0007](0007-migrations-sql-versionado.md) | Migrations em SQL versionado executado pelo dhctl | Accepted | db, deploy |
| [0008](0008-topologia-self-hosted-e-paridade-com-clickhouse-cloud.md) | Topologia self-hosted e paridade com ClickHouse Cloud | Accepted | deploy, avaliação GCP |
| [0009](0009-layout-de-pastas-context-first.md) | Layout de pastas context-first e ordenação de SQL por camada | Accepted | estrutura do repo |
| [0010](0010-observabilidade-e-qualidade-de-dados.md) | Observabilidade e qualidade de dados | Accepted | operação, confiança |
| [0011](0011-serving-aplicacional-e-analitico-no-mesmo-store.md) | Serving aplicacional e analítico no mesmo store | Accepted | design de chaves, projections |

## Referências externas usadas nas decisões

- ClickHouse Kafka Connect Sink — <https://clickhouse.com/docs/integrations/connectors/data-ingestion/kafka/kafka-clickhouse-connect-sink>
- ClickHouse Sink Connector no Confluent Cloud — <https://docs.confluent.io/cloud/current/connectors/cc-clickhouse-sink-connector/cc-clickhouse-sink.html>
- ClickPipes — schema registries — <https://clickhouse.com/docs/integrations/clickpipes/kafka/schema-registries>
- Formato AvroConfluent — <https://clickhouse.com/docs/interfaces/formats/AvroConfluent>
- Design do connector (exactly-once/KeeperMap) — <https://github.com/ClickHouse/clickhouse-kafka-connect/blob/main/docs/DESIGN.md>
