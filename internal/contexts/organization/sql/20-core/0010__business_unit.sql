-- GERADO (spike) a partir de contracts/domains/organization/business_unit.yaml
-- Core (L1): ReplacingMergeTree(_cdc_seq, _is_deleted) por chave de negocio
-- (ADR-0004 par3). ORDER BY e a chave de negocio -- nunca _cdc_seq aqui.
CREATE TABLE IF NOT EXISTS dh_core.organization__business_unit {ON_CLUSTER}
(
    bu_code              String,
    bu_name              String,
    channel              LowCardinality(String),
    region               LowCardinality(String),
    country              LowCardinality(String),
    cost_center          String,
    active               UInt8,
    created_at           DateTime64(6),
    updated_at           DateTime64(6),
    _cdc_seq            UInt64,
    _is_deleted         UInt8,
    _ingested_at        DateTime64(3)
)
ENGINE = ReplacingMergeTree(_cdc_seq, _is_deleted)
ORDER BY (bu_code);

-- MV de L0 -> L1: transformacao de linha unica, sem JOIN (ADR-0004 par3).
-- Idempotente: reprocessar L0 reinsere versoes que o ReplacingMergeTree
-- colapsa pela mesma _cdc_seq.
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_core.mv_organization__business_unit_l0_l1 {ON_CLUSTER}
TO dh_core.organization__business_unit
AS
SELECT
    bu_code,
    bu_name,
    channel,
    region,
    country,
    cost_center,
    active,
    created_at,
    updated_at,
    _cdc_seq,
    if(_op = 'd', 1, 0) AS _is_deleted,
    _ingested_at
FROM dh_landing.organization__business_unit_raw;

-- View corrente (ADR-0004 par4) -- L2/L3 NUNCA leem dh_core.organization__business_unit direto.
CREATE VIEW IF NOT EXISTS dh_core.v_organization__business_unit_current {ON_CLUSTER} AS
SELECT * FROM dh_core.organization__business_unit FINAL WHERE _is_deleted = 0;
