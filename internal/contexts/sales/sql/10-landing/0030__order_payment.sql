-- GERADO (spike) a partir de contracts/domains/sales/order_payment.yaml
-- Landing (L0): append-only, uma linha por versao de CDC recebida do topico
-- sap.sales.order_payment.v1. Nunca deduplica aqui (ADR-0004 par1).
CREATE TABLE IF NOT EXISTS dh_landing.sales__order_payment_raw {ON_CLUSTER}
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
    _topic               LowCardinality(String),
    _partition           UInt16,
    _offset              UInt64,
    _kafka_ts            DateTime64(3),
    _ingested_at         DateTime64(3) DEFAULT now64(3),
    _op                  Enum8('c'=1,'u'=2,'d'=3,'r'=4),
    _cdc_seq             UInt64,
    _schema_id           UInt32
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(_kafka_ts)
ORDER BY (payment_id, _cdc_seq)
TTL _ingested_at + INTERVAL 180 DAY;
