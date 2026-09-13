#!/usr/bin/env bash
set -euo pipefail

# Roda a matriz de verificacao do spike (ver
# docs/evaluation/spike-mock-flow-clickhouse-kafka.md) contra o ClickHouse do
# docker-compose, depois de 'make up', 'make db-apply' e 'make mock-load'.
#
# Cada bloco prova um mecanismo especifico do desenho (dedup, delete,
# ressurreicao, orfao, ASOF JOIN, dictionaries, funil de pagamento, mart de
# receita). Nao substitui os criterios de aceite formais das Fases 05-08 --
# e uma smoke suite rapida para confirmar que o ambiente local esta coerente
# com o desenho documentado.

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

ch() {
  docker compose exec -T clickhouse clickhouse-client \
    --user "$CH_USER" --password "$CH_PASSWORD" "$@"
}

sep() { echo; echo "===== $1 ====="; }

sep "1. Dedup ReplacingMergeTree + FINAL (ORD-OOO-1: v2 chegou antes da v1)"
ch --query "SELECT order_id, order_status, _cdc_seq FROM dh_core.v_sales__order_current WHERE order_id='ORD-OOO-1' FORMAT PrettyCompact"

sep "2. Delete via _is_deleted (ORD-DEL-1 nao deve aparecer na view corrente)"
ch --query "SELECT count() AS deve_ser_zero FROM dh_core.v_sales__order_current WHERE order_id='ORD-DEL-1' FORMAT PrettyCompact"

sep "3. Ressurreicao apos delete (ORD-RES-1 deve reaparecer como 'delivered')"
ch --query "SELECT order_id, order_status FROM dh_core.v_sales__order_current WHERE order_id='ORD-RES-1' FORMAT PrettyCompact"

sep "4. Orfao permanente (ORD-ORPHAN: item sem pedido, anti-join)"
ch --query "
SELECT i.order_id AS orphan_key, count() AS itens_orfaos
FROM dh_core.v_sales__order_item_current i
LEFT JOIN dh_core.v_sales__order_current o USING (order_id)
WHERE o.order_id = ''
GROUP BY orphan_key
ORDER BY orphan_key
FORMAT PrettyCompact"

sep "5. Normalizacao de cupom (achado, nao bug): PROMO10 / promo10 / ' PROMO10 '"
ch --query "
SELECT order_id, concat('[', discount_code, ']') AS discount_code_bruto,
       dictGetOrDefault('dh_core.dict__discount_codes','discount_type', tuple(discount_code), 'SEM_CADASTRO') AS resolved_type
FROM dh_core.v_sales__order_current
WHERE order_id IN ('ORD-000128','ORD-000129','ORD-000130','ORD-000131')
ORDER BY order_id
FORMAT PrettyCompact"

sep "6. dictGet/dictHas em dict__business_unit"
ch --query "SELECT dictGet('dh_core.dict__business_unit','bu_name', tuple('BU-01')) AS bu01_nome, dictHas('dh_core.dict__business_unit', tuple('BU-INEXISTENTE')) AS existe_bu_falsa FORMAT PrettyCompact"

sep "7. ASOF JOIN — preco vigente no instante do pedido (cenario 005)"
ch --query "
SELECT i.order_id, i.item_id, i.created_at, i.unit_price AS preco_praticado,
       pr.list_price AS preco_tabela_vigente, pr.valid_from AS preco_vigente_desde
FROM dh_core.v_sales__order_item_current i
ASOF LEFT JOIN dh_core.v_pricing__prices_current pr
  ON i.item_id = pr.item_id AND pr.valid_from <= i.created_at
WHERE i.item_id IN ('SKU-000001','SKU-000002')
ORDER BY i.item_id, i.created_at
FORMAT PrettyCompact"

sep "8. Lookup composto item_id+dc_id em stock_position (dc_id nulo em ORD-000126)"
ch --query "
SELECT i.order_id, i.item_id, i.dc_id, s.dc_name, s.available, s.safety_stock,
       if(s.item_id = '', 'SEM_POSICAO_DE_ESTOQUE', if(s.available < s.safety_stock, 'ABAIXO_DO_SAFETY_STOCK', 'OK')) AS situacao
FROM dh_core.v_sales__order_item_current i
LEFT JOIN dh_core.v_inventory__stock_position_current s
  ON i.item_id = s.item_id AND i.dc_id = s.dc_id
WHERE i.order_id IN ('ORD-000123','ORD-000125','ORD-000126')
ORDER BY i.order_id
FORMAT PrettyCompact"

sep "9. Disparando refresh dos marts Refreshable (payment_funnel_day, revenue_by_bu_day)"
ch --query "SYSTEM REFRESH VIEW dh_marts.mv__payment_funnel_day" || true
ch --query "SYSTEM REFRESH VIEW dh_marts.mv__revenue_by_bu_day" || true
for i in $(seq 1 20); do
  st=$(ch --query "SELECT any(status) FROM system.view_refreshes WHERE database='dh_marts' AND view='mv__payment_funnel_day'" 2>/dev/null || echo "")
  if [ "$st" != "RunningOnCluster" ] && [ "$st" != "Running" ] && [ -n "$st" ]; then
    echo "refresh payment_funnel_day status=$st (tentativa $i)"
    break
  fi
  sleep 1
done
ch --query "SELECT database, view, status, last_success_time, exception FROM system.view_refreshes WHERE database='dh_marts' FORMAT PrettyCompact"

sep "10. Funil de pagamento: ACQ-TEST dedup (esperado attempts=1, attempts_captured=1)"
ch --query "SELECT acquirer, attempts, attempts_captured FROM dh_marts.v_payment_funnel_day WHERE acquirer='ACQ-TEST' FORMAT PrettyCompact"

sep "11. Latencia com nulo (ACQ-LAT-TEST, esperado p50 ~10s, nao ~0)"
ch --query "SELECT acquirer, attempts, auth_latency_p50_s, auth_latency_max_s FROM dh_marts.v_payment_funnel_day WHERE acquirer='ACQ-LAT-TEST' FORMAT PrettyCompact"

sep "12. Fan-out de retentativa (ACQ-RETRY-TEST, esperado attempts=3, orders_with_attempt=1, max_attempts_per_order=3)"
ch --query "SELECT acquirer, attempts, orders_with_attempt, orders_captured, max_attempts_per_order, orders_with_retry FROM dh_marts.v_payment_funnel_day WHERE acquirer='ACQ-RETRY-TEST' FORMAT PrettyCompact"

sep "13. Mart Refreshable revenue_by_bu_day (receita por BU/dia)"
ch --query "SELECT business_unit_code, order_date, currency, orders, gross_revenue, net_revenue FROM dh_marts.v_revenue_by_bu_day ORDER BY business_unit_code, order_date FORMAT PrettyCompact"

sep "14. Contagens finais por camada"
ch --query "
SELECT database, table, count() AS active_parts, sum(rows) AS total_rows
FROM system.parts
WHERE database IN ('dh_landing','dh_core','dh_marts') AND active
GROUP BY database, table
ORDER BY database, table
FORMAT PrettyCompact"

sep "FIM DA VERIFICACAO"
