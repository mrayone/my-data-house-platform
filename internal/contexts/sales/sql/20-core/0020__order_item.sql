-- GERADO (spike) a partir de contracts/domains/sales/order_item.yaml
-- Core (L1): ReplacingMergeTree(_cdc_seq, _is_deleted) por chave de negocio
-- (ADR-0004 par3). ORDER BY e a chave de negocio -- nunca _cdc_seq aqui.
CREATE TABLE IF NOT EXISTS dh_core.sales__order_item {ON_CLUSTER}
(
    order_id             String,
    item_seq             Int32,
    item_id              String,
    dc_id                Nullable(String),
    quantity             Int32,
    unit_price           Decimal(18,4),
    gross_amount         Decimal(18,4),
    discount_amount      Decimal(18,4),
    total_amount         Decimal(18,4),
    created_at           DateTime64(6),
    updated_at           DateTime64(6),
    _cdc_seq            UInt64,
    _is_deleted         UInt8,
    _ingested_at        DateTime64(3)
)
ENGINE = ReplacingMergeTree(_cdc_seq, _is_deleted)
PARTITION BY toYYYYMM(created_at)
ORDER BY (order_id, item_seq);

-- MV de L0 -> L1: transformacao de linha unica, sem JOIN (ADR-0004 par3).
-- Idempotente: reprocessar L0 reinsere versoes que o ReplacingMergeTree
-- colapsa pela mesma _cdc_seq.
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_core.mv_sales__order_item_l0_l1 {ON_CLUSTER}
TO dh_core.sales__order_item
AS
SELECT
    order_id,
    item_seq,
    item_id,
    dc_id,
    quantity,
    unit_price,
    gross_amount,
    discount_amount,
    total_amount,
    created_at,
    updated_at,
    _cdc_seq,
    if(_op = 'd', 1, 0) AS _is_deleted,
    _ingested_at
FROM dh_landing.sales__order_item_raw;

-- View corrente (ADR-0004 par4) -- L2/L3 NUNCA leem dh_core.sales__order_item direto.
CREATE VIEW IF NOT EXISTS dh_core.v_sales__order_item_current {ON_CLUSTER} AS
SELECT * FROM dh_core.sales__order_item FINAL WHERE _is_deleted = 0;
