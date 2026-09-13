# `deploy/kafka/`

## Por que KRaft

Kafka roda em modo KRaft (broker + controller combinados, sem ZooKeeper) desde
a Fase 01. Confluent Platform 8.x já não suporta modo ZooKeeper — e mesmo se
suportasse, KRaft é o caminho de longo prazo do próprio Kafka. Ver
`docker-compose.yml` (serviço `kafka`) para a configuração de
`KAFKA_PROCESS_ROLES`, `KAFKA_CONTROLLER_QUORUM_VOTERS` e os listeners
(interno `PLAINTEXT` para os outros containers, `PLAINTEXT_HOST` em
`localhost:9092` para `producer`/`dhctl` rodando na máquina).

## Por que `auto.create.topics.enable=false`

Tópico nasce de `dhctl topics apply`, com as partições declaradas no contrato
YAML (`contracts/domains/<ctx>/<entidade>.yaml`) — ver
[ADR-0003](../../docs/adr/0003-extensibilidade-contract-first.md). Se o Kafka
criasse tópicos automaticamente na primeira mensagem, um contrato faltante ou
divergente passaria despercebido: o tópico existiria com partições
default, não com o que o contrato pede. Produzir num tópico inexistente
**deve falhar** — é o sinal de que o contrato não foi aplicado ainda.

## `topics.yaml`

Ainda não existe nesta fase. A partir da Fase 02/03, `dhctl generate` produz
`deploy/kafka/topics.yaml` a partir dos contratos (nome do tópico, partições,
`cleanup.policy`, `retention.ms`) e `dhctl topics apply` o lê para criar/
atualizar os tópicos via Admin API. **Não edite esse arquivo à mão** quando
ele existir — é gerado (`deploy/CLAUDE.md`).
