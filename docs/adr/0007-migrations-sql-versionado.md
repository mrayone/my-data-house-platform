# ADR-0007 — Aplicar mudanças de schema por migrations SQL versionadas executadas pelo dhctl

- **Status:** Accepted
- **Data:** 2026-09-13
- **Decisores:** Engenharia de Software, Engenharia de Dados
- **ADRs relacionados:** 0001, 0003, 0009

## Contexto

São quatro databases, ~30 entidades, e cada entidade traz tabela de landing,
tabela core, MV, view corrente, e participação em marts. Precisamos que:

- o estado do banco seja **derivável do repositório**;
- a mudança seja **revisável em PR**;
- reaplicar seja seguro (a PoC sobe e desce o ambiente muitas vezes);
- a ordem entre camadas seja respeitada (não se cria MV antes da tabela destino).

## Decisão

- **SQL puro, versionado, em arquivos.** Sem ORM, sem DSL, sem DDL em runtime.
- **Runner próprio:** `dhctl migrate` (`internal/migrate/`), que registra o
  aplicado em `dh_meta.schema_migrations(id, checksum, applied_at, duration_ms)`.
- **Idempotência obrigatória:** todo objeto criado com `IF NOT EXISTS`. Reaplicar
  a suíte inteira em banco já migrado é *no-op*.
- **Ordenação determinística por camada, depois contexto, depois sequência:**

  ```
  1. db/shared/00-bootstrap/*.sql                       (databases, dh_meta, roles)
  2. internal/contexts/<ctx>/sql/10-landing/*.sql       (L0)
  3. internal/contexts/<ctx>/sql/20-core/*.sql          (L1: tabela, MV, view corrente)
  4. internal/contexts/<ctx>/sql/30-marts/*.sql         (L2: dictionaries, refreshable MVs)
  5. internal/contexts/<ctx>/sql/40-reports/*.sql       (L3: views)
  ```

  Dentro da mesma camada, a ordem é `(contexto asc, nome do arquivo asc)`.
  O contexto `analytics` é ordenado **por último** dentro de cada camada, porque
  seus marts cruzam contextos.
- **ID da migration** = caminho relativo do arquivo. Estável, legível no log, e
  imune a renumeração — que é o ponto do ADR-0009: adicionar contexto não
  renumera nada.
- **Checksum** de cada arquivo aplicado é guardado. Alterar arquivo já aplicado
  faz `dhctl migrate` **falhar** com diff; a correção é nova migration. Exceção:
  `dhctl migrate --allow-drift` só em ambiente local e com aviso.
- **Sem rollback automático.** Reverter é uma migration nova. Na PoC, o caminho
  rápido é `make reset` (dropa `dh_*` e reaplica) — permitido porque L0 é
  reconstruível de Kafka/producer e L1/L2 de L0.
- **`ON CLUSTER`-ready:** todo DDL escrito com o placeholder
  `{ON_CLUSTER}` que o runner substitui por `ON CLUSTER '<nome>'` ou string vazia
  conforme config. Evita reescrever a suíte ao migrar para o Cloud (ADR-0008).
- **Um arquivo por objeto lógico**, nomeado `<seq4>__<descrição>.sql`, ex.:
  `20-core/0010__sales_order.sql`. Múltiplos statements no mesmo arquivo são
  permitidos quando formam uma unidade (tabela + MV + view corrente da mesma
  entidade), separados por `;` e executados na ordem.

## Alternativas consideradas

- **golang-migrate / goose:** maduros, mas orientados a OLTP com `up/down`;
  rollback não faz sentido aqui, e nenhum deles entende a ordenação por camada e
  contexto que o ADR-0009 exige. O runner é pequeno (~200 linhas) e ganha
  exatamente essas duas coisas.
- **Atlas / schema declarativo com diff automático:** atraente, mas o diff
  automático em MV e dictionary do ClickHouse é imprevisível, e queremos o DDL
  literal no PR.
- **DDL aplicado pela aplicação no boot:** invisível em review, sem histórico.
- **Scripts shell numerados globalmente:** força renumeração a cada contexto novo,
  gerando conflito de merge — exatamente o que o layout evita.

## Consequências

### Positivas
- Banco reproduzível do zero em um comando.
- Diff de PR mostra o DDL real.
- Adicionar contexto não toca em arquivo existente (zero conflito de merge).
- Migração para cluster/Cloud não exige reescrever DDL.

### Negativas / custo aceito
- Runner é código nosso para manter (Fase 02).
- Idempotência via `IF NOT EXISTS` significa que **alterar** coluna de tabela
  existente exige `ALTER` explícito em migration nova — `IF NOT EXISTS` não
  detecta divergência de definição. Mitigação: check em `make verify` comparando
  colunas esperadas (do contrato) com `system.columns`.

## Impacto no repositório

`db/shared/00-bootstrap/`, `internal/contexts/*/sql/*/`, `internal/migrate/`,
`cmd/dhctl` (`migrate`, `migrate --status`), `make migrate`, `make reset`.

## Como validar

```bash
make reset && make migrate            # do zero, verde
make migrate                          # segunda vez: 0 migrations aplicadas
dhctl migrate --status                 # lista aplicadas/pendentes na ordem canônica
# sabotagem: editar migration já aplicada -> dhctl migrate falha com diff
```
```sql
SELECT id, applied_at FROM dh_meta.schema_migrations ORDER BY applied_at;
```
