# `deploy/connect/`

## Versão do plugin

`ClickHouse/clickhouse-kafka-connect` **v1.5.0** — release oficial do GitHub
(https://github.com/ClickHouse/clickhouse-kafka-connect/releases), instalado
pelo `Dockerfile` deste diretório porque o connector não está publicado no
Confluent Hub. Registrado também em
[`../../docs/evaluation/clickhouse-cloud-gcp-criteria.md`](../../docs/evaluation/clickhouse-cloud-gcp-criteria.md).

Requisitos do connector (ADR-0002): ClickHouse ≥ 23.3, Kafka Connect ≥ 2.7,
Java 11+. A imagem base `confluentinc/cp-kafka-connect:8.5.0` cobre Connect e
Java; a versão do ClickHouse é a fixada em `docker-compose.yml` (26.8.3.105).

## Por que Kafka Connect e não Kafka Engine nativo do ClickHouse

Ver [ADR-0002](../../docs/adr/0002-ingestao-via-clickhouse-kafka-connect-sink.md)
— resumo: exactly-once real via KeeperMap, DLQ de primeira classe, e é o
caminho self-hosted mais próximo do ClickPipes (o equivalente gerenciado no
ClickHouse Cloud), o que preserva a validade da comparação desta PoC.

## Connectors que o `dhctl` gerencia

Nenhum ainda — `deploy/connect/connectors/*.json` é **gerado** por
`dhctl generate` a partir dos contratos (a partir da Fase 02) e aplicado via
`dhctl connectors apply` contra a REST API do Connect (`localhost:8083`).
**Não edite esses arquivos à mão** quando existirem (`deploy/CLAUDE.md`).

Cada connector gerado terá, no mínimo:
- `value.converter = io.confluent.connect.avro.AvroConverter` apontando para
  o Schema Registry;
- `exactlyOnce = true` (state store em KeeperMap);
- mapeamento estrito 1 tópico → 1 tabela de landing (`topic2TableMap`);
- `errors.tolerance = all` + `errors.deadletterqueue.topic.name` (DLQ
  obrigatória).

## Diagnóstico rápido

```bash
curl -s localhost:8083/connector-plugins | jq -r '.[].class' | grep -i clickhouse
# esperado: com.clickhouse.kafka.connect.ClickHouseSinkConnector

curl -s localhost:8083/connectors | jq
curl -s localhost:8083/connectors/<nome>/status | jq '.tasks[].state'
```

Problemas de plugin ausente, offset "preso" após rebobinar manualmente, ou DLQ
crescendo: ver
[`../../docs/runbooks/troubleshooting-ingestion.md`](../../docs/runbooks/troubleshooting-ingestion.md).
