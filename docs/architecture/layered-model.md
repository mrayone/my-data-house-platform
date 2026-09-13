# Modelo em camadas

Detalhamento operacional do [ADR-0001](../adr/0001-modelo-em-camadas-no-clickhouse.md).
Cada camada tem uma responsabilidade e, mais importante, uma lista do que ela
**não** faz.

| | L0 `dh_landing` | L1 `dh_core` | L2 `dh_marts` | L3 `dh_reports` |
|---|---|---|---|---|
| Grão | mensagem Kafka | entidade (chave de negócio) | assunto | recorte de leitura |
| Cardinalidade | 1 tabela por tópico | 1 tabela por entidade | 1 tabela por assunto | 1 view por caso de uso |
| Escrita por | Kafka Connect Sink | MV incremental de L0 | Refreshable MV / MV incremental / Dictionary | nada (view) |
| Mutável | não (append-only) | sim (dedup por versão) | recalculável | — |
| Quem lê | reprocesso e debug | apenas L2, via `v_*_current` | aplicação, BI, L3 | aplicação, BI |
| Retenção | TTL ≥ 180 dias (≥ Kafka) | estado atual | conforme assunto | — |

---

## L0 — Landing (`dh_landing`)

### Responsabilidade
Registrar, sem perda e sem interpretação, tudo o que chegou do Kafka.
**É a fonte de verdade reprocessável do sistema.**

### Forma
```sql
CREATE TABLE IF NOT EXISTS dh_landing.sales__order_raw {ON_CLUSTER}
(
    -- campos do contrato, tipados conforme o Avro
    order_id            String,
    customer_id         String,
    business_unit_code  LowCardinality(String),
    order_status        LowCardinality(String),
    total_amount        Decimal(18,4),
    created_at          DateTime64(3),
    updated_at          DateTime64(3),

    -- colunas técnicas obrigatórias (ADR-0004)
    _topic              LowCardinality(String),
    _partition          UInt16,
    _offset             UInt64,
    _kafka_ts           DateTime64(3),
    _ingested_at        DateTime64(3) DEFAULT now64(3),
    _op                 Enum8('c'=1,'u'=2,'d'=3,'r'=4),
    _cdc_seq            UInt64,
    _schema_id          UInt32
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(_kafka_ts)
ORDER BY (order_id, _cdc_seq)
TTL toDateTime(_kafka_ts) + INTERVAL 180 DAY
SETTINGS index_granularity = 8192;
```

Note que aqui `_cdc_seq` **está** no `ORDER BY` — em L0 queremos todas as
versões preservadas. Em L1 é o contrário (ver abaixo).

### O que L0 não faz
- Não deduplica.
- Não aplica delete.
- Não faz JOIN, não deriva coluna, não normaliza valor.
- Não é consultada por aplicação nem por BI (enforçado por grant, ADR-0011).

### Por que o TTL de L0 é maior que a retenção do Kafka
Se L0 expirar antes do Kafka, ainda podemos replayar. Se **as duas** expirarem,
perdemos a capacidade de reconstruir L1/L2 — e aí qualquer erro de modelagem
descoberto tarde é irreversível. O TTL é declarado por contrato
(`landing.ttl_days`) e `make verify` falha se algum contrato declarar menos de
90 dias.

---

## L1 — Core (`dh_core`)

### Responsabilidade
Responder "qual é o estado atual desta entidade", resolvendo dedup, ordem e
delete do CDC.

### Forma — três objetos por entidade, sempre

**1. A tabela**
```sql
CREATE TABLE IF NOT EXISTS dh_core.sales__order {ON_CLUSTER}
(
    order_id            String,
    customer_id         String,
    business_unit_code  LowCardinality(String),
    order_status        LowCardinality(String),
    total_amount        Decimal(18,4),
    created_at          DateTime64(3),
    updated_at          DateTime64(3),
    _cdc_seq            UInt64,
    _is_deleted         UInt8 DEFAULT 0,
    _ingested_at        DateTime64(3)
)
ENGINE = ReplacingMergeTree(_cdc_seq, _is_deleted)
PARTITION BY toYYYYMM(created_at)
ORDER BY (order_id);                 -- chave de NEGÓCIO, sem _cdc_seq
```

**2. A MV incremental que a alimenta** — linha a linha, sem JOIN:
```sql
CREATE MATERIALIZED VIEW IF NOT EXISTS dh_core.mv__sales__order_raw__to__order {ON_CLUSTER}
TO dh_core.sales__order AS
SELECT
    order_id, customer_id, business_unit_code, order_status,
    total_amount, created_at, updated_at,
    _cdc_seq,
    if(_op = 'd', 1, 0) AS _is_deleted,
    _ingested_at
FROM dh_landing.sales__order_raw;
```

**3. A view corrente — o único ponto de leitura permitido**
```sql
CREATE VIEW IF NOT EXISTS dh_core.v_sales__order_current {ON_CLUSTER} AS
SELECT * EXCEPT (_is_deleted)
FROM dh_core.sales__order FINAL
WHERE _is_deleted = 0;
```

### Por que a view é obrigatória
`ReplacingMergeTree` deduplica em merge **assíncrono, sem garantia de momento**.
`SELECT * FROM dh_core.sales__order` retorna duplicata e linha deletada. Não
"às vezes" — sempre que houver parts não mergeados, que é o estado normal de uma
tabela recebendo dados.

A view encapsula essa correção num único lugar. `scripts/checks/no-direct-core-read.sh`
falha o build se qualquer objeto de L2/L3 referenciar `dh_core.<entidade>` sem
passar pela view.

### Variante para varredura ampla
Para relatório que varre milhões de chaves, `FINAL` pode ser mais caro que
agregar. A forma permitida é:
```sql
SELECT order_id,
       argMax(order_status, _cdc_seq) AS order_status,
       argMax(total_amount, _cdc_seq) AS total_amount
FROM dh_core.sales__order
GROUP BY order_id
HAVING argMax(_is_deleted, _cdc_seq) = 0
```
Quando um mart usa essa forma, ele documenta a escolha no doc do cenário. É a
única exceção à regra "sempre pela view".

### O que L1 não faz
- Não correlaciona entidades (não há JOIN em L1).
- Não agrega.
- Não é consultada por BI diretamente (ADR-0011).

---

## L2 — Marts (`dh_marts`)

### Responsabilidade
Materializar o resultado de correlação e agregação por assunto, pronto para
consumo.

### Três formas, escolhidas pela árvore do [ADR-0005](../adr/0005-estrategia-de-agregacao-mv-refreshable-dictionary.md)

| Forma | Quando | Exemplo |
|---|---|---|
| **Refreshable MV** | correlação entre entidades mutáveis; qualquer coisa que dependa de dado que pode chegar depois | `dh_marts.order_360`, `dh_marts.revenue_by_bu_day` |
| **MV incremental → agregador** | fato append-only e imutável | contagem de eventos por minuto |
| **Dictionary** | cadastro pequeno consultado por chave | `dh_core.dict__business_unit`, `dh_core.dict__discount_codes` |

### Regras
1. Toda leitura de L1 é pela view `v_*_current`.
2. Refreshable MV grande recalcula **por janela** (padrão 3 dias), não a tabela
   inteira. A janela é a tolerância a *late arrival*.
3. Todo mart tem doc em [`../scenarios/`](../scenarios/) declarando: mecanismo,
   intervalo de refresh, janela, perfil de acesso e regras de negócio.
4. `ORDER BY` do mart é escolhido pelo perfil de acesso dominante
   ([ADR-0011](../adr/0011-serving-aplicacional-e-analitico-no-mesmo-store.md)).
5. Mart que cruza contextos vive em `internal/contexts/analytics/sql/30-marts/`.

### O que L2 não faz
- Não inventa regra que não esteja no doc do cenário.
- Não lê L0.

---

## L3 — Reports (`dh_reports`)

### Responsabilidade
Ser o **contrato de leitura**. Nomeia o caso de uso, recorta, filtra, formata.

```sql
CREATE VIEW IF NOT EXISTS dh_reports.v_revenue_by_bu_last_30d {ON_CLUSTER} AS
SELECT business_unit_code, bu_name, order_date,
       orders, gross_revenue, net_revenue, avg_ticket
FROM dh_marts.revenue_by_bu_day
WHERE order_date >= today() - 30;
```

Views parametrizadas quando o recorte é argumento:
```sql
CREATE VIEW IF NOT EXISTS dh_reports.v_order_360 {ON_CLUSTER} AS
SELECT * FROM dh_marts.order_360 WHERE order_id = {p_order_id:String};
```

### O que L3 não faz
- **Não contém lógica de negócio nova.** Se apareceu um cálculo aqui, ele
  pertence a L2. Isso é o que permite reescrever L2 sem quebrar cliente — e o
  contrário também: mudar a view sem medo de alterar um número.
- Não materializa nada.

---

## Reprocesso

O motivo de existirem quatro camadas.

| Cenário | Procedimento | Depende do Kafka? |
|---|---|---|
| Erro na MV L0→L1 | corrigir MV, `TRUNCATE dh_core.<e>`, `INSERT ... SELECT FROM dh_landing.<e>_raw` | não |
| Erro de regra num mart | corrigir SQL, `SYSTEM REFRESH VIEW dh_marts.mv__<m>` | não |
| Coluna nova num contrato | migration de `ALTER`, `dhctl generate`, replay do Kafka **ou** backfill de L0 se a coluna for derivável | só se não for derivável |
| Contrato errado desde o início | replay do Kafka | **sim** — é o único caso |

Runbook completo em [`../runbooks/reprocess.md`](../runbooks/reprocess.md).

---

## Resumo em uma tabela de proibições

| Proibido | Por quê | Check |
|---|---|---|
| `JOIN` em MV incremental | não dispara pelo lado direito; grava errado e não corrige | `no-join-in-incremental-mv.sh` |
| Ler `dh_core.<entidade>` sem `FINAL`/view | retorna duplicata e deletado | `no-direct-core-read.sh` |
| Agregador incremental sobre entidade mutável | dupla contagem em UPDATE de CDC | `no-incremental-agg-on-mutable.sh` |
| SQL de contexto referenciando outro contexto | quebra a fronteira do ADR-0009 | `no-cross-context-sql.sh` |
| `ALTER ... DELETE/UPDATE` como mecanismo de negócio | mutation não escala em taxa de CDC | revisão + `parity.sh` |
| Aplicação lendo `dh_landing` | expõe dado não deduplicado | grant + `grants.sh` |
| `Float*` em coluna monetária | erro de arredondamento em dinheiro | `dq.money.no_float` |
