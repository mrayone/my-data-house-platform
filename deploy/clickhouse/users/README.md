# `deploy/clickhouse/users/`

Reservado para os perfis de acesso do ADR-0011 §4 (`dh_app`, `dh_analyst`,
`dh_pipeline`). Ainda vazio na Fase 01 — a PoC usa o usuário `default` com
senha de `.env` até a camada de serving existir (Fase 07) e a necessidade de
isolar permissões por role ficar concreta.

Quando populado, cada arquivo é XML de perfil/usuário do ClickHouse, humano
(não gerado) — ver `deploy/CLAUDE.md`.
