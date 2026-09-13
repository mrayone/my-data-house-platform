-- Extraido de docs/scenarios/002-revenue-by-bu-day.md (fonte da verdade da
-- especificacao). Cenario 002: receita por unidade de negocio e dia.

CREATE TABLE IF NOT EXISTS dh_marts.revenue_by_bu_day {ON_CLUSTER}
(
    order_date            Date,
    business_unit_code    LowCardinality(String),
    bu_name               String,
    bu_channel            LowCardinality(String),
    bu_region             LowCardinality(String),
    order_channel         LowCardinality(String),
    currency              LowCardinality(String),

    orders                UInt64,
    orders_canceled       UInt64,
    units_sold            Int64,
    items_count           UInt64,
    distinct_customers    UInt64,

    gross_revenue         Decimal(38,4),
    discount_total        Decimal(38,4),
    freight_total         Decimal(38,4),
    net_revenue           Decimal(38,4),
    avg_ticket            Decimal(38,4),

    _bu_found             UInt8,          -- 0 = business_unit_code sem cadastro
    _refreshed_at         DateTime64(3)
)
ENGINE = ReplacingMergeTree(_refreshed_at)
PARTITION BY toYYYYMM(order_date)
ORDER BY (business_unit_code, order_date, order_channel, currency);

CREATE MATERIALIZED VIEW IF NOT EXISTS dh_marts.mv__revenue_by_bu_day {ON_CLUSTER}
REFRESH EVERY 15 MINUTE APPEND
TO dh_marts.revenue_by_bu_day AS
WITH
    (today() - 3) AS window_from,
    order_items AS (
        SELECT
            order_id,
            sum(quantity)   AS units,
            count()         AS items
        FROM dh_core.v_sales__order_item_current
        WHERE toDate(created_at) >= window_from
        GROUP BY order_id
    )
SELECT
    toDate(o.created_at)                                        AS order_date,
    o.business_unit_code                                        AS business_unit_code,
    dictGetOrDefault('dh_core.dict__business_unit', 'bu_name',
                     tuple(o.business_unit_code), '(sem cadastro)')   AS bu_name,
    dictGetOrDefault('dh_core.dict__business_unit', 'channel',
                     tuple(o.business_unit_code), 'unknown')          AS bu_channel,
    dictGetOrDefault('dh_core.dict__business_unit', 'region',
                     tuple(o.business_unit_code), 'unknown')          AS bu_region,
    o.channel                                                   AS order_channel,
    o.currency                                                  AS currency,
    countDistinct(o.order_id)                                   AS orders,
    countDistinctIf(o.order_id, o.order_status = 'canceled')    AS orders_canceled,
    sum(coalesce(oi.units, 0))                                  AS units_sold,
    sum(coalesce(oi.items, 0))                                  AS items_count,
    countDistinct(o.customer_id)                                AS distinct_customers,
    sumIf(o.gross_amount,    o.order_status != 'canceled')      AS gross_revenue,
    sumIf(o.discount_amount, o.order_status != 'canceled')      AS discount_total,
    sumIf(o.freight_amount,  o.order_status != 'canceled')      AS freight_total,
    sumIf(o.total_amount,    o.order_status != 'canceled')      AS net_revenue,
    if(countDistinctIf(o.order_id, o.order_status != 'canceled') = 0,
       toDecimal128(0, 4),
       sumIf(o.total_amount, o.order_status != 'canceled')
         / countDistinctIf(o.order_id, o.order_status != 'canceled')) AS avg_ticket,
    dictHas('dh_core.dict__business_unit', tuple(o.business_unit_code)) AS _bu_found,
    now64(3)                                                    AS _refreshed_at
FROM dh_core.v_sales__order_current AS o
LEFT JOIN order_items AS oi USING (order_id)
WHERE toDate(o.created_at) >= window_from
GROUP BY order_date, business_unit_code, bu_name, bu_channel, bu_region,
         order_channel, currency, _bu_found;

CREATE VIEW IF NOT EXISTS dh_marts.v_revenue_by_bu_day {ON_CLUSTER} AS
SELECT * EXCEPT (_refreshed_at) FROM dh_marts.revenue_by_bu_day FINAL;
