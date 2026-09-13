#!/usr/bin/env bash
set -euo pipefail

# Aplica bootstrap + landing + core + marts no ClickHouse do docker-compose.
#
# Concatena os arquivos SQL em ordem de dependencia (bootstrap -> landing ->
# core -> dictionaries de cadastro -> marts de analytics), substitui o
# placeholder {ON_CLUSTER} (ADR-0007/0008 -- vazio em topologia single-node) e
# executa via `docker compose exec clickhouse clickhouse-client`.
#
# Isto e ferramenta do spike registrado em
# docs/evaluation/spike-mock-flow-clickhouse-kafka.md -- NAO e o
# `dhctl migrate` real da Fase 02, que aplicara migrations versionadas
# geradas a partir dos contratos (contracts/domains/**/*.yaml). Enquanto o
# dhctl nao existe, este script e o jeito de subir o schema completo no
# ambiente local com uma unica ferramenta ja existente (docker compose +
# clickhouse-client), sem baixar nada a parte.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
fi

CH_USER="${CLICKHOUSE_USER:-default}"
CH_PASSWORD="${CLICKHOUSE_PASSWORD:-changeme}"

if [ -z "$(docker compose ps -q clickhouse 2>/dev/null)" ]; then
  echo "apply-ddl: o servico 'clickhouse' do docker compose nao esta rodando." >&2
  echo "apply-ddl: rode 'make up' primeiro." >&2
  exit 1
fi

combined="$(mktemp)"
trap 'rm -f "$combined"' EXIT

add() {
  for f in "$@"; do
    [ -f "$f" ] || continue
    {
      echo "-- ==== $f ===="
      sed 's/{ON_CLUSTER}//g' "$f"
      echo ""
    } >> "$combined"
  done
}

: > "$combined"
add db/shared/00-bootstrap/*.sql
for ctx in customer inventory organization pricing sales; do
  add internal/contexts/"$ctx"/sql/10-landing/*.sql
done
for ctx in customer inventory organization pricing sales; do
  add internal/contexts/"$ctx"/sql/20-core/*.sql
done
add internal/contexts/organization/sql/30-marts/*.sql
add internal/contexts/pricing/sql/30-marts/*.sql
add internal/contexts/analytics/sql/30-marts/*.sql

n_stmts=$(grep -c '^CREATE' "$combined" || true)
echo ">> aplicando $n_stmts instrucoes DDL no ClickHouse do docker compose..."
docker compose exec -T clickhouse clickhouse-client \
  --user "$CH_USER" --password "$CH_PASSWORD" --multiquery < "$combined"
echo ">> DDL aplicado sem erro."
