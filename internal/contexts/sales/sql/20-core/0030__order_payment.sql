-- GERADO (spike) a partir de contracts/domains/sales/order_payment.yaml
-- Core (L1): ReplacingMergeTree(_cdc_seq, _is_deleted) por chave de negocio
-- (ADR-0004 par3). ORDER BY e a chave de negocio -- nunca _cdc_seq aqui.
CREATE TABLE IF NOT EXISTS dh_core.sales__order_payment {ON_CLUSTER}
(
    payment_id           String,
    order_id             String,
    payment_method       LowCardinality(String),
    installments         Int32,
    payment_status       LowCardinality(String),
    amount               Decimal(18,4),
    acquirer             LowCardinality(String),
    authorized_at        Nullable(DateTime64(6)),
    captured_at          Nullable(DateTime64(6)),
    created_at           DateTime64(6),
    updated_at           DateTime64(6),
    _cdc_seq            UInt64,
    _is_deleted         UInt8,
    _ingested_at        DateTime64(3)
)
ENGINE = ReplacingMergeTree(_cdc_seq, _is_deleted)
PARTITION BY toYYYYMM(created_at)
ORDER BY (payment_id);

-- MV de L0 -> L1: transformacao de linha unica, sem JOIN (ADR-0004 par3).
-- Idempotente: reprocessar L0 reinsere versoes que o ReplacingMergeTree
-- colapsa pela mesma _cdc_seq.
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_core.mv_sales__order_payment_l0_l1 {ON_CLUSTER}
TO dh_core.sales__order_payment
AS
SELECT
    payment_id,
    order_id,
    payment_method,
    installments,
    payment_status,
    amount,
    acquirer,
    authorized_at,
    captured_at,
    created_at,
    updated_at,
    _cdc_seq,
    if(_op = 'd', 1, 0) AS _is_deleted,
    _ingested_at
FROM dh_landing.sales__order_payment_raw;

-- View corrente (ADR-0004 par4) -- L2/L3 NUNCA leem dh_core.sales__order_payment direto.
CREATE VIEW IF NOT EXISTS dh_core.v_sales__order_payment_current {ON_CLUSTER} AS
SELECT * FROM dh_core.sales__order_payment FINAL WHERE _is_deleted = 0;
