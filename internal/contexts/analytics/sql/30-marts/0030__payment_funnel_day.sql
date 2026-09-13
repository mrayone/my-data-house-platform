-- Extraido de docs/scenarios/003-payment-funnel.md (fonte da verdade da
-- especificacao). Cenario 003: funil e taxa de aprovacao de pagamento.

CREATE TABLE IF NOT EXISTS dh_marts.payment_funnel_day {ON_CLUSTER}
(
    attempt_date           Date,
    business_unit_code     LowCardinality(String),
    bu_name                String,
    order_channel          LowCardinality(String),
    payment_method         LowCardinality(String),
    acquirer               LowCardinality(String),
    installment_bucket     LowCardinality(String),
    currency               LowCardinality(String),

    attempts               UInt64,
    attempts_pending       UInt64,
    attempts_authorized    UInt64,
    attempts_captured      UInt64,
    attempts_denied        UInt64,
    attempts_refunded      UInt64,

    orders_with_attempt    UInt64,
    orders_captured        UInt64,

    amount_attempted       Decimal(38,4),
    amount_captured        Decimal(38,4),
    amount_refunded        Decimal(38,4),

    approval_rate          Float64,
    capture_rate           Float64,
    denial_rate            Float64,

    auth_latency_p50_s     Float64,
    auth_latency_p95_s     Float64,
    auth_latency_p99_s     Float64,
    auth_latency_max_s     Float64,

    max_attempts_per_order UInt32,
    orders_with_retry      UInt64,

    _refreshed_at          DateTime64(3)
)
ENGINE = ReplacingMergeTree(_refreshed_at)
PARTITION BY toYYYYMM(attempt_date)
ORDER BY (attempt_date, payment_method, acquirer, installment_bucket,
          business_unit_code, currency);

CREATE MATERIALIZED VIEW IF NOT EXISTS dh_marts.mv__payment_funnel_day {ON_CLUSTER}
REFRESH EVERY 5 MINUTE APPEND
TO dh_marts.payment_funnel_day AS
WITH
    (today() - 3) AS window_from,
    attempts_per_order AS (
        SELECT order_id, count() AS n
        FROM dh_core.v_sales__order_payment_current
        WHERE toDate(created_at) >= window_from
        GROUP BY order_id
    )
SELECT
    toDate(p.created_at)                                          AS attempt_date,
    o.business_unit_code                                          AS business_unit_code,
    dictGetOrDefault('dh_core.dict__business_unit', 'bu_name',
                     tuple(o.business_unit_code), '(sem cadastro)') AS bu_name,
    o.channel                                                     AS order_channel,
    p.payment_method                                              AS payment_method,
    p.acquirer                                                    AS acquirer,
    multiIf(p.installments <= 1,  '1',
            p.installments <= 6,  '2-6',
            p.installments <= 12, '7-12',
                                  '13+')                          AS installment_bucket,
    o.currency                                                    AS currency,

    count()                                                       AS attempts,
    countIf(p.payment_status = 'pending')                         AS attempts_pending,
    countIf(p.payment_status = 'authorized')                      AS attempts_authorized,
    countIf(p.payment_status = 'captured')                         AS attempts_captured,
    countIf(p.payment_status = 'denied')                          AS attempts_denied,
    countIf(p.payment_status = 'refunded')                        AS attempts_refunded,

    countDistinct(p.order_id)                                     AS orders_with_attempt,
    countDistinctIf(p.order_id, p.payment_status = 'captured')    AS orders_captured,

    sum(p.amount)                                                 AS amount_attempted,
    sumIf(p.amount, p.payment_status = 'captured')                AS amount_captured,
    sumIf(p.amount, p.payment_status = 'refunded')                AS amount_refunded,

    if(count() = 0, 0,
       countIf(p.payment_status IN ('authorized','captured')) / count())      AS approval_rate,
    if(countIf(p.payment_status IN ('authorized','captured')) = 0, 0,
       countIf(p.payment_status = 'captured')
         / countIf(p.payment_status IN ('authorized','captured')))            AS capture_rate,
    if(count() = 0, 0, countIf(p.payment_status = 'denied') / count())        AS denial_rate,

    quantileIf(0.50)(dateDiff('second', p.created_at, p.authorized_at),
                     p.authorized_at IS NOT NULL)                 AS auth_latency_p50_s,
    quantileIf(0.95)(dateDiff('second', p.created_at, p.authorized_at),
                     p.authorized_at IS NOT NULL)                 AS auth_latency_p95_s,
    quantileIf(0.99)(dateDiff('second', p.created_at, p.authorized_at),
                     p.authorized_at IS NOT NULL)                 AS auth_latency_p99_s,
    maxIf(dateDiff('second', p.created_at, p.authorized_at),
          p.authorized_at IS NOT NULL)                            AS auth_latency_max_s,

    max(coalesce(apo.n, 1))                                       AS max_attempts_per_order,
    countDistinctIf(p.order_id, coalesce(apo.n, 1) > 1)           AS orders_with_retry,

    now64(3)                                                      AS _refreshed_at

FROM dh_core.v_sales__order_payment_current AS p
INNER JOIN dh_core.v_sales__order_current AS o USING (order_id)
LEFT  JOIN attempts_per_order AS apo USING (order_id)
WHERE toDate(p.created_at) >= window_from
GROUP BY attempt_date, business_unit_code, bu_name, order_channel,
         payment_method, acquirer, installment_bucket, currency;

CREATE VIEW IF NOT EXISTS dh_marts.v_payment_funnel_day {ON_CLUSTER} AS
SELECT * EXCEPT (_refreshed_at) FROM dh_marts.payment_funnel_day FINAL;
