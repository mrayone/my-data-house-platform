-- GERADO (spike) a partir de contracts/domains/customer/customer.yaml
-- Landing (L0): append-only, uma linha por versao de CDC recebida do topico
-- sap.customer.customer.v1. Nunca deduplica aqui (ADR-0004 par1).
CREATE TABLE IF NOT EXISTS dh_landing.customer__customer_raw {ON_CLUSTER}
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
ORDER BY (customer_id, _cdc_seq)
TTL _ingested_at + INTERVAL 180 DAY;
