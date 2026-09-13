-- Dictionary de cadastro (ADR-0005) -- tira o join de organization/business_unit
-- do plano de execucao dos cenarios 002 e 004. Fonte: a view corrente de L1
-- (nunca a tabela core direto -- ADR-0004 par4).
CREATE DICTIONARY IF NOT EXISTS dh_core.dict__business_unit {ON_CLUSTER}
(
    bu_code     String,
    bu_name     String,
    channel     String,
    region      String,
    country     String,
    cost_center String,
    active      UInt8
)
PRIMARY KEY bu_code
SOURCE(CLICKHOUSE(
    QUERY 'SELECT bu_code, bu_name, channel, region, country, cost_center, active FROM dh_core.v_organization__business_unit_current'
))
LAYOUT(COMPLEX_KEY_HASHED())
LIFETIME(MIN 300 MAX 600);
