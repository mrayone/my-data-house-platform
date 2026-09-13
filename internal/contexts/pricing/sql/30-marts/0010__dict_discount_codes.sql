-- Dictionary de cadastro (ADR-0005). Cupom orfao (sales/order.discount_code
-- sem correspondente aqui) e esperado -- dictGetOrDefault resolve sem quebrar
-- (achado do cenario 004, nao um bug).
CREATE DICTIONARY IF NOT EXISTS dh_core.dict__discount_codes {ON_CLUSTER}
(
    discount_code       String,
    campaign_name       String,
    discount_type       String,
    discount_value      Decimal(18,4),
    valid_from          DateTime64(6),
    valid_to            DateTime64(6),
    max_uses            Int64,
    business_unit_scope Nullable(String)
)
PRIMARY KEY discount_code
SOURCE(CLICKHOUSE(
    USER 'dh_dict'
    PASSWORD ''
    QUERY 'SELECT discount_code, campaign_name, discount_type, discount_value, valid_from, valid_to, max_uses, business_unit_scope FROM dh_core.v_pricing__discount_codes_current'
))
LAYOUT(COMPLEX_KEY_HASHED())
LIFETIME(MIN 300 MAX 600);
