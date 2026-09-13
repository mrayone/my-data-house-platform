-- Databases da plataforma, um por camada (CLAUDE.md §7 / ADR-0001).
-- {ON_CLUSTER} é substituído pelo runner (dhctl migrate) por 'ON CLUSTER <nome>'
-- ou string vazia, conforme ADR-0007 — mantém o mesmo SQL rodando local e no
-- ClickHouse Cloud.
CREATE DATABASE IF NOT EXISTS dh_landing {ON_CLUSTER};
CREATE DATABASE IF NOT EXISTS dh_core    {ON_CLUSTER};
CREATE DATABASE IF NOT EXISTS dh_marts   {ON_CLUSTER};
CREATE DATABASE IF NOT EXISTS dh_reports {ON_CLUSTER};
