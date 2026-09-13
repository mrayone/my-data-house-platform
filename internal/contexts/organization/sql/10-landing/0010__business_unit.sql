-- GERADO (spike) a partir de contracts/domains/organization/business_unit.yaml
-- Landing (L0): append-only, uma linha por versao de CDC recebida do topico
-- sap.organization.business_unit.v1. Nunca deduplica aqui (ADR-0004 par1).
CREATE TABLE IF NOT EXISTS dh_landing.organization__business_unit_raw {ON_CLUSTER}
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
ORDER BY (bu_code, _cdc_seq)
TTL _ingested_at + INTERVAL 365 DAY;
