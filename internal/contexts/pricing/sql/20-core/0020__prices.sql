-- GERADO (spike) a partir de contracts/domains/pricing/prices.yaml
-- Core (L1): ReplacingMergeTree(_cdc_seq, _is_deleted) por chave de negocio
-- (ADR-0004 par3). ORDER BY e a chave de negocio -- nunca _cdc_seq aqui.
CREATE TABLE IF NOT EXISTS dh_core.pricing__prices {ON_CLUSTER}
(
    item_id              String,
    price_list_id        LowCardinality(String),
    currency             LowCardinality(String),
    list_price           Decimal(18,4),
    cost_price           Decimal(18,4),
    valid_from           DateTime64(6),
    valid_to             Nullable(DateTime64(6)),
    created_at           DateTime64(6),
    updated_at           DateTime64(6),
    _cdc_seq            UInt64,
    _is_deleted         UInt8,
    _ingested_at        DateTime64(3)
)
ENGINE = ReplacingMergeTree(_cdc_seq, _is_deleted)
PARTITION BY toYYYYMM(valid_from)
ORDER BY (item_id, price_list_id, valid_from);

-- MV de L0 -> L1: transformacao de linha unica, sem JOIN (ADR-0004 par3).
-- Idempotente: reprocessar L0 reinsere versoes que o ReplacingMergeTree
-- colapsa pela mesma _cdc_seq.
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_core.mv_pricing__prices_l0_l1 {ON_CLUSTER}
TO dh_core.pricing__prices
AS
SELECT
    item_id,
    price_list_id,
    currency,
    list_price,
    cost_price,
    valid_from,
    valid_to,
    created_at,
    updated_at,
    _cdc_seq,
    if(_op = 'd', 1, 0) AS _is_deleted,
    _ingested_at
FROM dh_landing.pricing__prices_raw;

-- View corrente (ADR-0004 par4) -- L2/L3 NUNCA leem dh_core.pricing__prices direto.
CREATE VIEW IF NOT EXISTS dh_core.v_pricing__prices_current {ON_CLUSTER} AS
SELECT * FROM dh_core.pricing__prices FINAL WHERE _is_deleted = 0;
