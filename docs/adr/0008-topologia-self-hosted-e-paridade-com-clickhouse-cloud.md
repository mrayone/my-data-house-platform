# ADR-0008 — Rodar self-hosted na PoC preservando paridade explícita com ClickHouse Cloud no GCP

- **Status:** Accepted
- **Data:** 2026-09-13
- **Decisores:** Engenharia de Dados, Engenharia de Software
- **ADRs relacionados:** 0002, 0005, 0007, 0010

## Contexto

A PoC é self-hosted **porque o objetivo é decidir se vale contratar** o
ClickHouse Cloud no GCP — contratar antes de decidir inverteria a ordem. Mas uma
PoC self-hosted que use recursos que o Cloud não oferece (ou vice-versa) produz
uma conclusão inválida.

Diferenças reais que importam:

| Aspecto | Self-hosted (PoC) | ClickHouse Cloud |
|---|---|---|
| Armazenamento | disco local, `MergeTree` clássico | separação compute/storage, object storage |
| Replicação | `ReplicatedMergeTree` + Keeper (se cluster) | gerenciada, transparente |
| Ingestão Kafka | Kafka Connect Sink (ADR-0002) | **ClickPipes** ou connector gerenciado |
| DDL distribuído | `ON CLUSTER` manual | implícito |
| Settings experimentais | livres | **restritos** |
| Escala | vertical no nó | autoscaling |

## Decisão

### Topologia da PoC
Single-node, via `docker-compose.yml`:

`clickhouse-server` (1) · `clickhouse-keeper` (1, necessário para KeeperMap do
exactly-once — ADR-0002) · `kafka` (1, KRaft) · `schema-registry` (1) ·
`kafka-connect` (1, imagem própria com o plugin) · `grafana` + exportador de
métricas (opcional, Fase 08).

### Regras de paridade (obrigatórias)

1. **Nenhuma dependência de caminho de arquivo local** no modelo de dados:
   sem `File`/`URL` engine, sem `MergeTree` com disco nomeado específico.
2. **Todo DDL usa o placeholder `{ON_CLUSTER}`** (ADR-0007), então migrar para
   cluster não reescreve SQL.
3. **Toda feature com *setting* experimental é registrada** em
   `docs/evaluation/clickhouse-cloud-gcp-criteria.md` com a pergunta
   "disponível no Cloud?" em aberto até verificação. Hoje isso inclui, no mínimo,
   **Refreshable Materialized View** (ADR-0005) e **KeeperMap** (ADR-0002) — são
   os dois pilares que precisam de confirmação explícita antes da contratação.
4. **A camada de ingestão é a única peça que assumimos substituir** na migração:
   Connect → ClickPipes. Como a ingestão só escreve em L0 sem transformar
   (ADR-0002), a substituição não toca L0/L1/L2/L3. Esse é o principal argumento
   de redução de risco da contratação.
5. **Nada de `ALTER ... UPDATE/DELETE` como mecanismo de negócio** (ADR-0004):
   mutation é pior no Cloud que localmente.
6. **Versão do ClickHouse fixada por digest** em `docker-compose.yml`, e a mesma
   major registrada no doc de avaliação, para que a comparação tenha sentido.
7. **Benchmark com dataset dimensionado e documentado** (Fase 08): volume,
   cardinalidade e taxa de ingestão declarados, para poder reexecutar no Cloud e
   comparar maçã com maçã.

### O que a PoC deliberadamente NÃO tenta provar

- Custo do Cloud (é cotação, não medição) — o que medimos é **volume, taxa,
  latência e CPU/RAM necessários**, que alimentam a cotação.
- Comportamento de autoscaling.
- SLA e disaster recovery gerenciados.

Isso fica explícito em `docs/evaluation/` para que a decisão não seja tomada com
base numa medição que não existe.

## Alternativas consideradas

- **PoC direto no ClickHouse Cloud (trial):** valida o produto real e o
  ClickPipes de verdade. Descartado como caminho principal porque exige
  contratação/trial antes da decisão e não permite medir consumo de recurso do
  nó. **Recomendado como Fase 09 opcional**, reaproveitando 100% dos contratos e
  migrations — está previsto em `docs/plan/implementation-plan.md`.
- **Cluster multi-nó self-hosted na PoC:** mais parecido com produção, muito mais
  operação para a pergunta em jogo. O `{ON_CLUSTER}` deixa a porta aberta.
- **ClickHouse gerenciado por terceiro (ex.: Altinity no GCP):** alternativa
  legítima de contratação; incluída como coluna comparativa em
  `docs/evaluation/`, não como ambiente da PoC.

## Consequências

### Positivas
- Decisão de contratar fica baseada em medição própria.
- Migração para o Cloud muda **uma** peça (ingestão), não o modelo.
- `{ON_CLUSTER}` e as regras de paridade evitam retrabalho.

### Negativas / custo aceito
- Números de performance do single-node não se traduzem linearmente para o Cloud.
  Assumido e declarado no doc de avaliação.
- Se Refreshable MV ou KeeperMap não estiverem disponíveis no Cloud como
  esperado, há retrabalho — por isso são os dois primeiros itens a verificar,
  já na Fase 01, antes de construir sobre eles.

## Impacto no repositório

`docker-compose.yml`, `deploy/clickhouse/`, `deploy/keeper/`, `deploy/connect/`,
`docs/evaluation/`, `internal/migrate/` (`{ON_CLUSTER}`).

## Como validar

```sql
SELECT version();
SELECT name, value FROM system.settings
WHERE name IN ('allow_experimental_refreshable_materialized_view');
SELECT count() FROM system.tables WHERE create_table_query ILIKE '%engine = file%';  -- 0
```
```bash
grep -rL '{ON_CLUSTER}' --include='*.sql' internal/contexts db | grep -v 40-reports || true
scripts/checks/parity.sh
```
