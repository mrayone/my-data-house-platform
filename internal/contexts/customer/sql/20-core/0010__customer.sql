-- GERADO (spike) a partir de contracts/domains/customer/customer.yaml
-- Core (L1): ReplacingMergeTree(_cdc_seq, _is_deleted) por chave de negocio
-- (ADR-0004 par3). ORDER BY e a chave de negocio -- nunca _cdc_seq aqui.
CREATE TABLE IF NOT EXISTS dh_core.customer__customer {ON_CLUSTER}
(
    customer_id          String,
    document_hash        String,
    first_name           String,
    email_hash           String,
    customer_since       Date32,
    segment              LowCardinality(String),
    loyalty_tier         LowCardinality(String),
    city                 String,
    state                LowCardinality(String),
    zipcode              String,
    created_at           DateTime64(6),
    updated_at           DateTime64(6),
    _cdc_seq            UInt64,
    _is_deleted         UInt8,
    _ingested_at        DateTime64(3)
)
ENGINE = ReplacingMergeTree(_cdc_seq, _is_deleted)
PARTITION BY toYYYYMM(customer_since)
ORDER BY (customer_id);

-- MV de L0 -> L1: transformacao de linha unica, sem JOIN (ADR-0004 par3).
-- Idempotente: reprocessar L0 reinsere versoes que o ReplacingMergeTree
-- colapsa pela mesma _cdc_seq.
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_core.mv_customer__customer_l0_l1 {ON_CLUSTER}
TO dh_core.customer__customer
AS
SELECT
    customer_id,
    document_hash,
    first_name,
    email_hash,
    customer_since,
    segment,
    loyalty_tier,
    city,
    state,
    zipcode,
    created_at,
    updated_at,
    _cdc_seq,
    if(_op = 'd', 1, 0) AS _is_deleted,
    _ingested_at
FROM dh_landing.customer__customer_raw;

-- View corrente (ADR-0004 par4) -- L2/L3 NUNCA leem dh_core.customer__customer direto.
CREATE VIEW IF NOT EXISTS dh_core.v_customer__customer_current {ON_CLUSTER} AS
SELECT * FROM dh_core.customer__customer FINAL WHERE _is_deleted = 0;
