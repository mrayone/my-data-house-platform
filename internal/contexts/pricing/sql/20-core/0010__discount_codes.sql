-- GERADO (spike) a partir de contracts/domains/pricing/discount_codes.yaml
-- Core (L1): ReplacingMergeTree(_cdc_seq, _is_deleted) por chave de negocio
-- (ADR-0004 par3). ORDER BY e a chave de negocio -- nunca _cdc_seq aqui.
CREATE TABLE IF NOT EXISTS dh_core.pricing__discount_codes {ON_CLUSTER}
(
    discount_code        String,
    campaign_name        String,
    discount_type        LowCardinality(String),
    discount_value       Decimal(18,4),
    valid_from           DateTime64(6),
    valid_to             DateTime64(6),
    max_uses             Int64,
    business_unit_scope  Nullable(String),
    created_at           DateTime64(6),
    updated_at           DateTime64(6),
    _cdc_seq            UInt64,
    _is_deleted         UInt8,
    _ingested_at        DateTime64(3)
)
ENGINE = ReplacingMergeTree(_cdc_seq, _is_deleted)
ORDER BY (discount_code);

-- MV de L0 -> L1: transformacao de linha unica, sem JOIN (ADR-0004 par3).
-- Idempotente: reprocessar L0 reinsere versoes que o ReplacingMergeTree
-- colapsa pela mesma _cdc_seq.
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_core.mv_pricing__discount_codes_l0_l1 {ON_CLUSTER}
TO dh_core.pricing__discount_codes
AS
SELECT
    discount_code,
    campaign_name,
    discount_type,
    discount_value,
    valid_from,
    valid_to,
    max_uses,
    business_unit_scope,
    created_at,
    updated_at,
    _cdc_seq,
    if(_op = 'd', 1, 0) AS _is_deleted,
    _ingested_at
FROM dh_landing.pricing__discount_codes_raw;

-- View corrente (ADR-0004 par4) -- L2/L3 NUNCA leem dh_core.pricing__discount_codes direto.
CREATE VIEW IF NOT EXISTS dh_core.v_pricing__discount_codes_current {ON_CLUSTER} AS
SELECT * FROM dh_core.pricing__discount_codes FINAL WHERE _is_deleted = 0;
