# CLAUDE.md — db/

## O que fica aqui

**Somente o SQL que não pertence a nenhum contexto.** Hoje: `db/shared/00-bootstrap/`.

```
db/shared/00-bootstrap/
├── 0010__databases.sql   <- dh_landing, dh_core, dh_marts, dh_reports, dh_meta
├── 0020__roles.sql       <- roles dh_app, dh_analyst, dh_pipeline e seus grants
├── 0030__dh_meta.sql     <- schema_migrations, dq_checks, dq_check_results, pipeline_health
└── 0031__dq_catalog.sql  <- seed do catálogo de checks (Fase 08)
```

## O que NÃO fica aqui

**O SQL de cada contexto**, que vive em `internal/contexts/<ctx>/sql/<camada>/`.
Isso não é acidente de organização: é o [ADR-0009](../docs/adr/0009-layout-de-pastas-context-first.md).
Numeração global de migration faria duas branches que adicionam entidades diferentes
colidirem no mesmo número — exatamente o cenário deste projeto, com vários agentes
em paralelo.

## Ordem canônica de execução

Derivada pelo runner, não mantida à mão
([ADR-0007](../docs/adr/0007-migrations-sql-versionado.md)):

```
1. db/shared/00-bootstrap/*.sql
2. internal/contexts/<ctx>/sql/10-landing/*.sql     <- GERADO
3. internal/contexts/<ctx>/sql/20-core/*.sql
4. internal/contexts/<ctx>/sql/30-marts/*.sql
5. internal/contexts/<ctx>/sql/40-reports/*.sql
```

Dentro de cada camada: contextos em ordem alfabética, **com `analytics` por último**
(seus marts cruzam contextos). Dentro do contexto: por nome de arquivo.

O **ID** da migration é o caminho relativo do arquivo — estável, legível no log, e
imune a renumeração.

## Regras

1. **Idempotência obrigatória:** todo objeto com `CREATE ... IF NOT EXISTS`.
   Reaplicar a suíte inteira em banco migrado é *no-op*.
2. **`{ON_CLUSTER}` em todo DDL**, exceto `40-reports/` (views). O runner substitui
   por `ON CLUSTER '<nome>'` ou string vazia. É o que evita reescrever a suíte ao
   migrar para o Cloud ([ADR-0008](../docs/adr/0008-topologia-self-hosted-e-paridade-com-clickhouse-cloud.md)).
3. **Migration aplicada não se edita.** O checksum é verificado; alterar faz
   `dhctl migrate` falhar. Correção é migration nova.
4. **`IF NOT EXISTS` não detecta divergência de definição.** Alterar coluna de
   tabela existente exige `ALTER` explícito em migration nova.
5. **Sem rollback automático.** Reverter é migration nova. Na PoC, `make reset`
   dropa `dh_*` e reaplica — permitido porque L0 é reconstruível do Kafka e L1/L2 de
   L0.
6. **Nada de `ALTER TABLE ... DELETE/UPDATE` como mecanismo de negócio.** Mutation
   não escala em taxa de CDC; delete é coluna
   ([ADR-0004](../docs/adr/0004-semantica-cdc-dedup-ordem-delete.md) §5).
7. **`dh_app` nunca recebe grant em `dh_landing`**, e em `dh_core` só nas views
   `v_*` (`scripts/checks/grants.sh` verifica).
8. **Um arquivo por objeto lógico**, nomeado `<seq4>__<descrição>.sql`. Múltiplos
   statements são permitidos quando formam uma unidade (tabela + MV + view corrente
   da mesma entidade).

## Os cinco databases

| Database | Camada | Quem escreve | Quem lê |
|---|---|---|---|
| `dh_landing` | L0 | Kafka Connect Sink | só reprocesso e debug |
| `dh_core` | L1 | MV incremental de L0 | L2, **sempre pelas views `v_*_current`** |
| `dh_marts` | L2 | Refreshable MV / MV incremental / Dictionary | aplicação, BI, L3 |
| `dh_reports` | L3 | nada (views) | aplicação, BI |
| `dh_meta` | — | `dhctl` | operação e avaliação |
