#!/usr/bin/env bash
set -euo pipefail

# Gera o dataset sintetico completo (cmd/producer mock) e carrega cada
# arquivo NDJSON na tabela dh_landing correspondente do ClickHouse do
# docker-compose, via `clickhouse-client ... FORMAT JSONEachRow`.
#
# Stand-in explicito para o producer Avro + Kafka Connect Sink reais da
# Fase 03 (ver docs/evaluation/spike-mock-flow-clickhouse-kafka.md) -- mesmo
# layout de colunas final (landing/core/marts), transporte diferente. Util
# como fonte de dados de teste deterministica mesmo depois que o transporte
# real existir.

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
OUT_DIR="${MOCK_OUT_DIR:-./out/mock}"
BASE_TS="${MOCK_BASE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"

if [ -z "$(docker compose ps -q clickhouse 2>/dev/null)" ]; then
  echo "load: o servico 'clickhouse' do docker compose nao esta rodando." >&2
  echo "load: rode 'make up' e 'make db-apply' primeiro." >&2
  exit 1
fi

echo ">> compilando producer..."
mkdir -p bin
go build -o bin/producer ./cmd/producer

echo ">> gerando dataset mock (base=$BASE_TS) em $OUT_DIR..."
./bin/producer mock --out "$OUT_DIR" --base "$BASE_TS"

# Ordem de carga: organization/pricing/customer/inventory (cadastro) antes de
# sales, e order_item antes de order dentro de sales -- exercita de proposito
# a orfandade transitoria do cenario 009 (ADR-0004 §7): o anti-join so
# resolve depois que os dois lados de cada topico terminam de carregar.
FILES=(
  organization__business_unit.jsonl
  pricing__discount_codes.jsonl
  pricing__prices.jsonl
  customer__customer.jsonl
  inventory__stock_position.jsonl
  sales__order_item.jsonl
  sales__order.jsonl
  sales__order_payment.jsonl
)

table_for() {
  case "$1" in
    customer__customer.jsonl) echo dh_landing.customer__customer_raw ;;
    inventory__stock_position.jsonl) echo dh_landing.inventory__stock_position_raw ;;
    organization__business_unit.jsonl) echo dh_landing.organization__business_unit_raw ;;
    pricing__discount_codes.jsonl) echo dh_landing.pricing__discount_codes_raw ;;
    pricing__prices.jsonl) echo dh_landing.pricing__prices_raw ;;
    sales__order_item.jsonl) echo dh_landing.sales__order_item_raw ;;
    sales__order.jsonl) echo dh_landing.sales__order_raw ;;
    sales__order_payment.jsonl) echo dh_landing.sales__order_payment_raw ;;
    *) echo "load: arquivo sem tabela mapeada: $1" >&2; exit 1 ;;
  esac
}

for f in "${FILES[@]}"; do
  table="$(table_for "$f")"
  echo ">> carregando $f -> $table"
  docker compose exec -T clickhouse clickhouse-client \
    --user "$CH_USER" --password "$CH_PASSWORD" \
    --query "INSERT INTO $table FORMAT JSONEachRow" < "$OUT_DIR/$f"
  n=$(docker compose exec -T clickhouse clickhouse-client \
    --user "$CH_USER" --password "$CH_PASSWORD" \
    --query "SELECT count() FROM $table")
  echo "   $table: $n linhas"
done

cat <<'EOM'

>> carga completa. Para atualizar os marts Refreshable sob demanda (sem
   esperar o intervalo de REFRESH):
     docker compose exec clickhouse clickhouse-client --query "SYSTEM REFRESH VIEW dh_marts.mv__revenue_by_bu_day"
     docker compose exec clickhouse clickhouse-client --query "SYSTEM REFRESH VIEW dh_marts.mv__payment_funnel_day"

   Ou rode 'make mock-verify' para a matriz de verificacao completa do spike.
EOM
