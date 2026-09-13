# Como os dados são armazenados no ClickHouse

O [`layered-model.md`](layered-model.md) define a **forma lógica** de cada
camada (o que cada tabela significa e quem pode ler). Este doc desce um nível:
como esses dados existem fisicamente — engines, parts, partições, índice,
merges, TTL e onde cada coisa vive em disco.

## 1. Os quatro databases e suas engines

| Database | Papel | Engine dominante | Por quê |
|---|---|---|---|
| `dh_landing` | histórico bruto reprocessável | `MergeTree` | append-only puro; nunca deduplica |
| `dh_core` | estado atual por entidade | `ReplacingMergeTree(_cdc_seq, _is_deleted)` | dedup por versão de CDC + delete lógico |
| `dh_marts` | agregados por assunto | `ReplacingMergeTree(_refreshed_at)` | cada refresh substitui o anterior |
| `dh_reports` | contrato de leitura | `View` (nada materializado) | recorte sem cópia |

Fora das tabelas, dois objetos com armazenamento próprio:

- **Dictionaries** (`dict__business_unit`, `dict__discount_codes`): cópia do
  cadastro **em memória** (`COMPLEX_KEY_HASHED`), recarregada do core a cada
  5-10 min. Não têm parts em disco — morrem e renascem no reload.
- **KeeperMap** (estado do exactly-once do connector): pares chave-valor
  gravados **no ClickHouse Keeper**, não no MergeTree — sobrevivem a restart
  do Connect e são o que impede reprocessamento duplicado
  ([ADR-0002](../adr/0002-ingestao-via-clickhouse-kafka-connect-sink.md)).

## 2. A física do MergeTree

Todo INSERT cria um **part**: um diretório imutável com um arquivo por coluna,
comprimido (lz4 por default). Nada é atualizado in-place — "mudar" uma linha é
inserir outra versão e deixar o merge (ou a leitura) resolver.

```
/var/lib/clickhouse/data/dh_landing/sales__order_raw/
├── 202609_1_1_0/          ← part: partição 202609, primeiro INSERT
│   ├── order_id.bin       ← coluna comprimida
│   ├── order_id.cmrk3     ← marcas do índice esparso
│   ├── ...                ← um par por coluna
│   ├── primary.cidx       ← índice primário (ORDER BY)
│   └── partition.dat
├── 202609_2_2_0/          ← part: segundo INSERT
└── 202609_1_2_1/          ← merge dos dois (os originais somem depois)
```

Três consequências práticas:

1. **INSERTs pequenos e frequentes criam parts demais** ("too many parts") —
   por isso o connector insere em batch, e a MV incremental L0→L1 acompanha o
   INSERT (síncrona), não linha a linha.
2. **Merge é assíncrono e sem garantia de momento.** É no merge que o
   `ReplacingMergeTree` descarta versões antigas — logo uma tabela core
   fisicamente contém duplicatas quase sempre. É a razão de existir a view
   `v_*_current` (`FINAL` + `WHERE _is_deleted = 0`).
3. **Delete de CDC não apaga nada**: vira `_is_deleted = 1` numa versão nova.
   Remoção física só em `OPTIMIZE ... FINAL CLEANUP` — até lá, quem esconde a
   linha é a view.

## 3. Particionamento e ordenação — as duas decisões de layout

```sql
PARTITION BY toYYYYMM(_kafka_ts)      -- landing: partição mensal
ORDER BY (order_id, _cdc_seq)         -- landing: todas as versões juntas
```

**`PARTITION BY`** decide em que diretório o part nasce. Partição mensal dá
*pruning* (query com filtro de data pula meses inteiros) e TTL barato (expirar
= dropar diretório). Granularidade maior que a mensal criaria parts demais.

**`ORDER BY`** decide a ordenação física dentro do part e é o **índice
primário** — esparso: uma marca a cada `index_granularity` (8192) linhas.
Buscar `order_id = 'X'` lê só os granules que podem conter X. É a decisão que
faz o acesso por chave das aplicações funcionar
([ADR-0011](../adr/0011-serving-aplicacional-e-analitico-no-mesmo-store.md)) —
e ela muda de sentido por camada:

| Camada | ORDER BY | Intenção |
|---|---|---|
| landing | `(chave, _cdc_seq)` | preservar e localizar **todas** as versões |
| core | `(chave)` — **sem** `_cdc_seq` | 1 linha lógica por chave; o Replacing colapsa dentro da chave |
| mart | chave de acesso do consumidor (ex.: `(business_unit_code, order_date, ...)`) | o recorte que o relatório/API pede |

Colunas de baixa cardinalidade (`status`, `currency`, `channel`) usam
`LowCardinality(String)` — dicionário interno por part, menos bytes e
comparações mais rápidas. Dinheiro é sempre `Decimal(18,4)` (L0/L1) ou
`Decimal(38,4)` (agregados) — nunca `Float` (`dq.money.no_float`).

## 4. Retenção (TTL)

TTL declarado por contrato (`landing.ttl_days`) e aplicado por partição:

| Tabela | TTL | Racional |
|---|---|---|
| `sales__*_raw`, `customer__*_raw` | 180 dias | ≥ retenção do Kafka: enquanto L0 viver, L1/L2 são reconstruíveis sem replay |
| `organization/pricing *_raw` | 365 dias | cadastro muda pouco, histórico barato |
| `inventory__stock_position_raw` | 90 dias | posição de estoque envelhece rápido |
| core / marts | sem TTL | estado atual + agregados recalculáveis |

## 5. Onde cada coisa vive no ambiente local

| Volume Docker | Montado em | Contém |
|---|---|---|
| `clickhouse-data` | `/var/lib/clickhouse` | parts de todos os `dh_*`, metadados, dicionários de LowCardinality |
| `keeper-data` | `/var/lib/clickhouse-keeper` | log Raft + snapshots: estado do KeeperMap (exactly-once) |
| `kafka-data` | `/var/lib/kafka/data` | segmentos dos tópicos `sap.*`, DLQs e internos do Connect |

`make down` preserva os três; `make reset-env` zera tudo. Perder
`keeper-data` sem perder `kafka-data` é o cenário do replay travado
([data-flow §3.7](data-flow.md)): o estado do connector some, os offsets não.

## 6. O que medir para a decisão Cloud

O layout físico é o que a avaliação do ClickHouse Cloud precifica: bytes
comprimidos por camada, razão de compressão, parts ativos e custo de `FINAL`.
As consultas de medição e os números ficam em
[`../evaluation/`](../evaluation/) (`make bench`); tamanho por tabela sai de
`system.parts` — a matriz do `make mock-verify` imprime a contagem por camada
ao final.
