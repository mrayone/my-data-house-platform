#!/usr/bin/env bash
set -euo pipefail

# Aplica todos os connectors de deploy/connect/connectors/*.json no Kafka
# Connect (REST 8083), substituindo ${CLICKHOUSE_PASSWORD} pelo valor do
# ambiente/.env (deploy/CLAUDE.md #1: nenhum segredo versionado).
# Idempotente: PUT /connectors/<name>/config cria ou atualiza.
#
# Spike da Fase 04: o definitivo e `dhctl connectors apply`.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
fi

CONNECT_URL="${CONNECT_URL:-http://localhost:8083}"
export CLICKHOUSE_PASSWORD="${CLICKHOUSE_PASSWORD:-changeme}"

shopt -s nullglob
files=(deploy/connect/connectors/*.json)
if [ ${#files[@]} -eq 0 ]; then
  echo "apply-connectors: nenhum connector em deploy/connect/connectors/" >&2
  exit 1
fi

for f in "${files[@]}"; do
  name="$(python3 -c "import json,sys; print(json.load(open(sys.argv[1]))['name'])" "$f")"
  echo ">> aplicando connector $name ($f)"
  python3 - "$f" <<'EOF' | curl -fsS -X PUT -H "Content-Type: application/json" \
      --data-binary @- "$CONNECT_URL/connectors/$name/config" > /dev/null
import json, os, sys
cfg = json.load(open(sys.argv[1]))["config"]
cfg["password"] = os.environ["CLICKHOUSE_PASSWORD"] if cfg.get("password") == "${CLICKHOUSE_PASSWORD}" else cfg.get("password", "")
print(json.dumps(cfg))
EOF
done

echo
echo ">> status dos connectors:"
sleep 3
for f in "${files[@]}"; do
  name="$(python3 -c "import json,sys; print(json.load(open(sys.argv[1]))['name'])" "$f")"
  curl -fsS "$CONNECT_URL/connectors/$name/status" | python3 -c "
import json, sys
s = json.load(sys.stdin)
tasks = ','.join(t['state'] for t in s.get('tasks', [])) or 'SEM-TASK'
print(f\"   {s['name']}: connector={s['connector']['state']} tasks={tasks}\")
"
done
