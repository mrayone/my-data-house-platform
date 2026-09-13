-- GERADO (spike) a partir de contracts/domains/inventory/stock_position.yaml
-- Landing (L0): append-only, uma linha por versao de CDC recebida do topico
-- sap.inventory.stock_position.v1. Nunca deduplica aqui (ADR-0004 par1).
CREATE TABLE IF NOT EXISTS dh_landing.inventory__stock_position_raw {ON_CLUSTER}
(
    item_id              String,
    dc_id                LowCardinality(String),
    dc_name              String,
    on_hand              Int64,
    reserved             Int64,
    available            Int64,
    safety_stock         Int64,
    position_at          DateTime64(6),
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
ORDER BY (item_id, dc_id, _cdc_seq)
TTL _ingested_at + INTERVAL 90 DAY;
