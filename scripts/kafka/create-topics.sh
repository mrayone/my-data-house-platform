#!/usr/bin/env bash
set -euo pipefail

# Cria os topicos Kafka de todas as entidades contratadas, com as particoes
# declaradas em contracts/domains/<ctx>/<entidade>.yaml, mais a DLQ de cada
# um (deploy/CLAUDE.md #5). Idempotente: --if-not-exists.
#
# Spike da Fase 03: extrai topico/particoes dos contratos por grep; o
# provisionamento definitivo e `dhctl topics apply` (Fase 02), que faz o
# parse completo do contrato.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

if [ -z "$(docker compose ps -q kafka 2>/dev/null)" ]; then
  echo "create-topics: o servico 'kafka' do docker compose nao esta rodando. Rode 'make up'." >&2
  exit 1
fi

kt() {
  docker compose exec -T kafka kafka-topics --bootstrap-server localhost:9092 "$@"
}

for contract in contracts/domains/*/*.yaml; do
  topic="$(grep -E '^\s*topic:' "$contract" | head -1 | awk '{print $2}')"
  partitions="$(grep -E '^\s*partitions:' "$contract" | head -1 | awk '{print $2}')"
  if [ -z "$topic" ] || [ -z "$partitions" ]; then
    echo "create-topics: $contract sem topic/partitions — pulando" >&2
    continue
  fi
  echo ">> topico $topic ($partitions particoes)"
  kt --create --if-not-exists --topic "$topic" --partitions "$partitions" --replication-factor 1
  echo ">> DLQ dh-dlq-$topic (1 particao)"
  kt --create --if-not-exists --topic "dh-dlq-$topic" --partitions 1 --replication-factor 1
done

echo
echo ">> topicos existentes:"
kt --list
