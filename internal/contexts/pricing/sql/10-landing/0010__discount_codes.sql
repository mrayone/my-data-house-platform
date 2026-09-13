-- GERADO (spike) a partir de contracts/domains/pricing/discount_codes.yaml
-- Landing (L0): append-only, uma linha por versao de CDC recebida do topico
-- sap.pricing.discount_codes.v1. Nunca deduplica aqui (ADR-0004 par1).
CREATE TABLE IF NOT EXISTS dh_landing.pricing__discount_codes_raw {ON_CLUSTER}
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
ORDER BY (discount_code, _cdc_seq)
TTL _ingested_at + INTERVAL 365 DAY;
