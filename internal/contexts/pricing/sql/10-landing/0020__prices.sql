-- GERADO (spike) a partir de contracts/domains/pricing/prices.yaml
-- Landing (L0): append-only, uma linha por versao de CDC recebida do topico
-- sap.pricing.prices.v1. Nunca deduplica aqui (ADR-0004 par1).
CREATE TABLE IF NOT EXISTS dh_landing.pricing__prices_raw {ON_CLUSTER}
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
ORDER BY (item_id, price_list_id, valid_from, _cdc_seq)
TTL _ingested_at + INTERVAL 365 DAY;
