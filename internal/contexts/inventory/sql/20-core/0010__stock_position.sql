-- GERADO (spike) a partir de contracts/domains/inventory/stock_position.yaml
-- Core (L1): ReplacingMergeTree(_cdc_seq, _is_deleted) por chave de negocio
-- (ADR-0004 par3). ORDER BY e a chave de negocio -- nunca _cdc_seq aqui.
CREATE TABLE IF NOT EXISTS dh_core.inventory__stock_position {ON_CLUSTER}
(
    item_id              String,
    dc_id                LowCardinality(String),
    dc_name              String,
    on_hand              Int64,
    reserved             Int64,
    available            Int64,
    safety_stock         Int64,
    position_at          DateTime64(6),
    created_at           DateTime64(6),
    updated_at           DateTime64(6),
    _cdc_seq            UInt64,
    _is_deleted         UInt8,
    _ingested_at        DateTime64(3)
)
ENGINE = ReplacingMergeTree(_cdc_seq, _is_deleted)
PARTITION BY toYYYYMM(position_at)
ORDER BY (item_id, dc_id);

-- MV de L0 -> L1: transformacao de linha unica, sem JOIN (ADR-0004 par3).
-- Idempotente: reprocessar L0 reinsere versoes que o ReplacingMergeTree
-- colapsa pela mesma _cdc_seq.
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_core.mv_inventory__stock_position_l0_l1 {ON_CLUSTER}
TO dh_core.inventory__stock_position
AS
SELECT
    item_id,
    dc_id,
    dc_name,
    on_hand,
    reserved,
    available,
    safety_stock,
    position_at,
    created_at,
    updated_at,
    _cdc_seq,
    if(_op = 'd', 1, 0) AS _is_deleted,
    _ingested_at
FROM dh_landing.inventory__stock_position_raw;

-- View corrente (ADR-0004 par4) -- L2/L3 NUNCA leem dh_core.inventory__stock_position direto.
CREATE VIEW IF NOT EXISTS dh_core.v_inventory__stock_position_current {ON_CLUSTER} AS
SELECT * FROM dh_core.inventory__stock_position FINAL WHERE _is_deleted = 0;
