# ADR-0004 — Resolver CDC (dedup, fora de ordem e delete) na camada core com ReplacingMergeTree versionado

- **Status:** Accepted
- **Data:** 2026-09-13
- **Decisores:** Engenharia de Dados
- **ADRs relacionados:** 0001, 0002, 0005, 0010

## Contexto

A origem é CDC. Isso significa três problemas que **não** existem em ingestão de
eventos de negócio append-only:

1. **A mesma chave chega N vezes.** Um pedido que muda de status 5 vezes gera 5
   mensagens com o mesmo `order_id`. O "estado atual" é a última versão, e a
   noção de "última" não é a ordem de chegada.
2. **Eventos chegam fora de ordem.** Entre tópicos sempre (partições e
   connectors independentes: o `order_item` pode aterrissar antes do `order`).
   Dentro do mesmo tópico, em rebalance, replay ou quando o Datasphere reenvia
   um snapshot.
3. **Existe DELETE.** O SAP apaga registros, e o ClickHouse Kafka Connect Sink
   **não executa delete** (ADR-0002). Se ignorarmos, dado apagado na origem
   continua contando no relatório para sempre.

Some-se a isso a armadilha que envenena a maioria das tentativas: **agregado
incremental sobre dado mutável conta duas vezes.** Um `SummingMergeTree`
alimentado por MV que dispara a cada insert de L0 soma a v1 e a v2 do mesmo
pedido. O total fica errado, e fica errado de forma plausível — o pior tipo de
erro.

## Decisão

### 1. L0 é append-only e guarda toda versão

Landing nunca deduplica. Ela registra, por linha, os metadados de controle:

| Coluna | Tipo | Origem |
|---|---|---|
| `_topic` | `LowCardinality(String)` | Kafka |
| `_partition` | `UInt16` | Kafka |
| `_offset` | `UInt64` | Kafka |
| `_kafka_ts` | `DateTime64(3)` | Kafka |
| `_ingested_at` | `DateTime64(3)` | `now64(3)` default |
| `_op` | `Enum8('c'=1,'u'=2,'d'=3,'r'=4)` | CDC (`r` = snapshot/read) |
| `_cdc_seq` | `UInt64` | CDC — **monotônica por chave** |
| `_schema_id` | `UInt32` | Schema Registry |

### 2. `_cdc_seq` é a autoridade de ordem — nunca o tempo de chegada

`_cdc_seq` vem do CDC (LSN/commit sequence do Datasphere). É o **único**
critério de "mais novo". Regra de precedência ao derivar:

```
version = _cdc_seq
tie-break (mesmo _cdc_seq): (_kafka_ts, _partition, _offset)
```

Contrato sem `version_column` confiável é um contrato inválido: `dhctl contract
validate` falha. Se a origem não oferece sequência, o fallback declarado no
contrato é `updated_at` convertido para microssegundos — e isso vai documentado
no contrato como risco, porque timestamp empata.

### 3. L1 core: `ReplacingMergeTree(_cdc_seq, _is_deleted)` por chave de negócio

```sql
CREATE TABLE IF NOT EXISTS dh_core.sales__order
(
    order_id            String,
    customer_id         String,
    business_unit_code  LowCardinality(String),
    order_status        LowCardinality(String),
    currency            LowCardinality(String),
    total_amount        Decimal(18,4),
    created_at          DateTime64(3),
    updated_at          DateTime64(3),
    _cdc_seq            UInt64,
    _is_deleted         UInt8,
    _ingested_at        DateTime64(3)
)
ENGINE = ReplacingMergeTree(_cdc_seq, _is_deleted)
PARTITION BY toYYYYMM(created_at)
ORDER BY (order_id);
```

- `ORDER BY` = **chave de negócio**, nada de `_cdc_seq` aqui (se `_cdc_seq`
  entrar no `ORDER BY`, cada versão é uma linha distinta e nada deduplica).
- MV de L0→L1 é **transformação de linha única, sem JOIN**:
  `_op = 'd'` → `_is_deleted = 1`; demais → `0`.
- A MV é idempotente: reprocessar L0 reinsere versões que o
  `ReplacingMergeTree` colapsa pela mesma `_cdc_seq`.

### 4. Leitura correta de L1 é obrigatória — três formas permitidas

O `ReplacingMergeTree` deduplica em merge **assíncrono e sem garantia de
momento**. Ler `SELECT * FROM dh_core.sales__order` é **errado** e retorna
duplicata e linha deletada.

| Forma | Quando usar | Custo |
|---|---|---|
| `FINAL` | consulta pontual por chave (caso aplicacional) | aceitável com `ORDER BY` bem escolhido |
| `argMax(col, _cdc_seq) ... GROUP BY <chave>` + `HAVING argMax(_is_deleted,_cdc_seq)=0` | varredura analítica ampla | melhor que `FINAL` em volume |
| View `dh_core.v_<entidade>_current` | **padrão do projeto** | encapsula a escolha acima |

**Regra:** L2 e L3 nunca referenciam a tabela `dh_core.<entidade>` direto —
sempre a view `dh_core.v_<entidade>_current`. Isso é verificável (§ Como validar)
e é a defesa contra o erro mais comum do projeto.

Defina também `SET final = 1` como *setting* no perfil do usuário de aplicação
(`deploy/clickhouse/users/`) como cinto de segurança, não como substituto da view.

### 5. Delete é coluna, nunca `DELETE`

- CDC `_op='d'` → `_is_deleted = 1`, com o `_cdc_seq` do delete.
- Nenhum `ALTER TABLE ... DELETE` no caminho normal (mutation é caro e assíncrono).
- `_is_deleted` só é **removido fisicamente** com `OPTIMIZE ... FINAL CLEANUP`;
  até então a linha existe e a view `v_<entidade>_current` é o que a esconde.
- Reviver chave depois de delete funciona: um `_cdc_seq` maior com
  `_is_deleted = 0` vence.

### 6. Agregação: aditivo só sobre fato imutável

| Natureza do dado | Estratégia |
|---|---|
| Fato **append-only e imutável** (ex.: tentativa de pagamento como evento) | MV incremental → `SummingMergeTree`/`AggregatingMergeTree`. Rápido e correto. |
| Entidade **mutável** (`order`, `order_item`, `customer`, `prices`, `stock_position`) | **Nunca** agregado incremental direto de L0. Agrega de `v_<entidade>_current` via **Refreshable MV** (ADR-0005). |

**Proibição explícita:** nenhuma MV incremental pode ter como destino um engine
agregador lendo de uma tabela de L0 de entidade mutável. É a causa raiz de
dupla contagem. `make verify` inclui um check estático que procura esse padrão.

### 7. Chegada fora de ordem entre entidades: tolerar, medir, nunca descartar

Item de pedido que chega antes do pedido **não é erro** — é normal. Portanto:

- Nenhum passo do pipeline descarta linha órfã.
- Marts que correlacionam usam Refreshable MV, que reavalia periodicamente e
  **se autocorrige** quando o outro lado chega (ADR-0005).
- A orfandade é **medida** como métrica de qualidade (ADR-0010) e tem relatório
  próprio: `docs/scenarios/009-*.md`. Órfão persistente é incidente; órfão
  transitório é o funcionamento esperado.
- Marts têm janela de *late arrival* declarada (padrão: 72h) durante a qual a
  partição é recalculada.

## Alternativas consideradas

### A) `CollapsingMergeTree` / `VersionedCollapsingMergeTree`
- **Prós:** desenhado para sinal `+1/-1`, ideal com *before image* de CDC.
- **Contras:** exige que **todo** UPDATE traga o estado anterior completo e
  corretamente pareado. Se o Datasphere não garantir isso em 100% dos casos (e
  não temos como garantir na PoC), o estado fica permanentemente desbalanceado, e
  o erro é difícil de detectar.
- **Por que não:** frágil em relação a uma garantia que não controlamos.
  `VersionedCollapsingMergeTree` mitiga a ordem, mas não o pareamento.

### B) Dedup na ingestão (connector ou consumer Go)
- **Prós:** core já chega limpo; leitura trivial.
- **Contras:** exige estado por chave no ingestor; o connector não faz isso
  (ADR-0002); e "limpo" seria só para a ordem em que os dados chegaram — replay
  reintroduz o problema.
- **Por que não:** dedup precisa de ordem global por chave, que só existe depois
  que todo mundo chegou. É naturalmente uma operação de leitura, não de escrita.

### C) `ArgMax` em tudo, sem `ReplacingMergeTree`
- **Prós:** sempre correto, nenhuma dependência de merge.
- **Contras:** custo de `GROUP BY` na chave em toda consulta; inviabiliza o
  padrão aplicacional de busca por chave com latência baixa.
- **Por que não:** não atende o requisito aplicacional. Mantido como a forma
  permitida para varredura ampla (§4).

### D) Mutations (`ALTER TABLE ... DELETE/UPDATE`)
- **Por que não:** assíncronas, reescrevem parts inteiras, não escalam em taxa
  de CDC. Antipadrão no ClickHouse.

## Consequências

### Positivas
- Corretude independe de o merge ter rodado.
- Delete e ressurreição funcionam sem mutation.
- Reprocesso de L0 é idempotente por construção.
- Fora de ordem deixa de ser bug e passa a ser métrica.

### Negativas / custo aceito
- `FINAL` custa CPU; a escolha de `ORDER BY` passa a ser crítica (ADR-0011).
- L1 guarda todas as versões até o merge, então L1 é maior que o estado lógico.
- A view `v_<entidade>_current` é uma indireção obrigatória que gente nova vai
  esquecer. Mitigado por check automático em `make verify`.
- Refreshable MV introduz latência (intervalo de refresh) nos marts
  correlacionados. Medir e registrar em `docs/evaluation/`.

### Riscos e mitigação
| Risco | Mitigação |
|---|---|
| `_cdc_seq` não monotônica na origem real | `make verify` roda check de monotonicidade por chave em L0 e alerta |
| Alguém consultar `dh_core.<entidade>` sem `FINAL` | Check estático em `make verify` + grants por database |
| Dupla contagem por MV incremental sobre entidade mutável | Check estático do padrão proibido em `make verify` |
| Órfão permanente confundido com transitório | Relatório 009 com SLA de idade do órfão |

## Impacto no repositório

- `internal/contexts/<ctx>/sql/10-landing/` — colunas `_*` obrigatórias.
- `internal/contexts/<ctx>/sql/20-core/` — tabela core + MV + `v_*_current`.
- `internal/codegen/ddl/` — injeta as colunas técnicas.
- `scripts/checks/` — checks estáticos de `make verify`.
- `docs/scenarios/009-*.md` — relatório de qualidade/orfandade.

## Como validar

```sql
-- 1) dedup efetiva: nenhuma chave com >1 linha na view corrente
SELECT count() FROM (
  SELECT order_id, count() c FROM dh_core.v_sales__order_current
  GROUP BY order_id HAVING c > 1
);            -- esperado: 0

-- 2) delete respeitado
SELECT count() FROM dh_core.v_sales__order_current
WHERE order_id IN (SELECT order_id FROM dh_landing.sales__order_raw WHERE _op='d');
-- esperado: 0 (salvo ressurreição com _cdc_seq maior)

-- 3) vence a maior _cdc_seq, não a última a chegar
--    (o producer injeta propositalmente v2 antes de v1 — ver test/e2e)
SELECT order_status FROM dh_core.v_sales__order_current WHERE order_id='OOO-TEST-1';
-- esperado: o status da maior _cdc_seq

-- 4) monotonicidade de _cdc_seq por chave em L0
SELECT count() FROM (
  SELECT order_id, anyIf(1, _cdc_seq = 0) z FROM dh_landing.sales__order_raw
  GROUP BY order_id HAVING z > 0
);            -- esperado: 0
```

```bash
scripts/checks/no-direct-core-read.sh          # L2/L3 não leem dh_core.<entidade> direto
scripts/checks/no-incremental-agg-on-mutable.sh
```
