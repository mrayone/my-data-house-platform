# Fluxo de dados

O caminho de uma mensagem, e — mais útil — o que acontece nos casos difíceis.
A visão inversa — como **vários** tópicos convergem num agregado — está em
[`fan-in.md`](fan-in.md); a física do armazenamento, em [`storage.md`](storage.md).

## 1. Caminho feliz

```
SAP  ──CDC──>  Datasphere  ──Avro+SR──>  Confluent topic
                                              │
                                    Kafka Connect Sink
                                    · AvroConverter resolve schema_id no SR
                                    · key = chave de negócio (particionamento);
                                      o envelope CDC viaja no value
                                    · batch -> INSERT
                                    · offset + estado no KeeperMap (exactly-once)
                                              ▼
                             dh_landing.sales__order_raw   (L0, append-only)
                                              │  MV incremental dispara no INSERT
                                              ▼
                             dh_core.sales__order          (L1, Replacing)
                                              │  leitura sempre por
                                              ▼
                             dh_core.v_sales__order_current
                                              │  Refreshable MV (cada 1 min, janela 3d)
                                              ▼
                             dh_marts.order_360            (L2)
                                              │
                                              ▼
                             dh_reports.v_order_360        (L3)
                                              │
                                    cmd/api  GET /orders/{id}/360
```

## 2. Mapa de relações entre as entidades da PoC

```
                    ┌──────────────────────┐
                    │   business_unit      │  (cadastro pequeno → DICTIONARY)
                    │   PK bu_code         │
                    └──────────▲───────────┘
                     business_unit_code    │  join por COLUNA, não por ID surrogate
                    ┌──────────┴───────────┐
   ┌───────────────>│       order          │<──────────────┐
   │  customer_id   │   PK order_id        │   order_id    │
   │                └──────────┬───────────┘               │
┌──┴──────────┐               │ order_id          ┌────────┴────────┐
│  customer   │               ▼                   │ order_payment   │
│ PK cust_id  │     ┌──────────────────┐          │ PK payment_id   │ 1:N
└─────────────┘     │   order_item     │          └─────────────────┘
                    │ PK (order_id,    │
                    │     item_seq)    │
                    └───┬──────────┬───┘
              item_id   │          │  (item_id, dc_id)   ← chave COMPOSTA
        ┌───────────────▼──┐    ┌──▼──────────────────┐
        │     prices       │    │  stock_position     │
        │ PK (item_id,     │    │  PK (item_id,dc_id) │
        │  price_list_id,  │    └─────────────────────┘
        │  valid_from)     │            ▲
        └──────────────────┘            │ conectam-se entre si SÓ por item_id,
                 ▲                      │ sem passar por order → cenário 010
                 └──────────────────────┘

        ┌────────────────────┐
        │  discount_codes    │  order.discount_code → discount_code
        │  PK discount_code  │  join por CÓDIGO (string), cupom pode não existir
        └────────────────────┘  no cadastro → achado de qualidade (cenário 004)
```

### As cinco formas de correlação, e onde cada uma aparece

| Forma | Exemplo | Mecanismo | Cenário |
|---|---|---|---|
| Por ID | `order` ↔ `order_item` ↔ `order_payment` | Refreshable MV | 001, 003 |
| Por coluna não-ID | `order.business_unit_code` → `business_unit.bu_code`; `order.discount_code` | Dictionary + `dictGet` | 002, 004 |
| Chave composta | `order_item(item_id, dc_id)` ↔ `stock_position(item_id, dc_id)` | Refreshable MV | 006 |
| Temporal / intervalo | `order_item.created_at` vs `prices.valid_from` | `ASOF JOIN` | 005 |
| **Ausência** de relação | item vendido sem posição de estoque; item com preço e estoque sem venda | anti-join | 007, 009, 010 |

Índice completo em [`../scenarios/README.md`](../scenarios/README.md).

## 3. Os casos difíceis

### 3.1 A mesma chave chega cinco vezes

Um pedido que muda de status gera cinco mensagens com o mesmo `order_id`.

```
L0:  order_id=A  _cdc_seq=100  status=created
     order_id=A  _cdc_seq=140  status=paid
     order_id=A  _cdc_seq=155  status=invoiced
     order_id=A  _cdc_seq=190  status=shipped
     order_id=A  _cdc_seq=240  status=delivered      ← 5 linhas, todas preservadas

L1:  ReplacingMergeTree(_cdc_seq, _is_deleted) ORDER BY (order_id)
     ↳ lógicamente 1 linha: a de _cdc_seq=240
     ↳ FISICAMENTE ainda 5, até o merge rodar

v_..._current:  FINAL + WHERE _is_deleted=0  →  1 linha, status=delivered  ✓
```

**Ler `dh_core.sales__order` direto aqui devolveria 5 linhas.** É por isso que a
view é obrigatória ([ADR-0004 §4](../adr/0004-semantica-cdc-dedup-ordem-delete.md)).

### 3.2 Chegou fora de ordem

```
ordem de chegada:  _cdc_seq=240 (delivered)   depois   _cdc_seq=190 (shipped)
```
`ReplacingMergeTree(_cdc_seq)` mantém a **maior versão**, não a última inserida.
Resultado: `delivered`. Correto, e sem nenhum código de compensação.

A ordem de chegada só é usada como desempate quando `_cdc_seq` é igual:
`(_kafka_ts, _partition, _offset)`.

### 3.3 O item chegou antes do pedido

Este **não é erro** — é o comportamento normal de connectors independentes.

```
t0   dh_landing.sales__order_item_raw  ← item do pedido B
t0   dh_landing.sales__order_raw       ← (vazio para B)

t1   Refreshable MV roda:  order_360 não tem linha para B
     → B é contado como ÓRFÃO, e sua idade começa a ser medida

t2   dh_landing.sales__order_raw       ← pedido B chega

t3   Refreshable MV roda de novo:  order_360 agora tem B, completo    ✓
     → sem intervenção, sem script de correção
```

É exatamente por isso que o mart usa **Refreshable MV** e não MV incremental com
JOIN: a MV incremental teria gravado B incompleto em t1 e **nunca** corrigiria
([ADR-0005](../adr/0005-estrategia-de-agregacao-mv-refreshable-dictionary.md)).

A orfandade transitória é medida, não tratada como erro. Órfão que persiste além
da janela de refresh é incidente — cenário 009.

### 3.4 O SAP apagou o registro

O Kafka Connect Sink **não executa DELETE**. Então:

```
L0:  order_id=A  _cdc_seq=300  _op='d'
       │  MV: if(_op='d', 1, 0)
L1:  order_id=A  _cdc_seq=300  _is_deleted=1     ← linha existe, marcada
v_..._current:  WHERE _is_deleted=0              → A não aparece   ✓
```

Remoção física só em `OPTIMIZE ... FINAL CLEANUP`. Até lá, a view é o que
esconde. **Ressurreição funciona:** `_cdc_seq=350` com `_is_deleted=0` vence.

### 3.5 Por que não se agrega direto de L0 uma entidade mutável

O erro mais perigoso do projeto, porque o número errado parece plausível.

```
L0 recebe:   order_id=A  _cdc_seq=100  total=100.00
             order_id=A  _cdc_seq=140  total=150.00     (cliente adicionou item)

MV incremental → SummingMergeTree:
             dispara no insert 1: soma 100.00
             dispara no insert 2: soma 150.00
             TOTAL = 250.00                             ✗ ERRADO

Refreshable MV lendo v_sales__order_current:
             lê 1 linha (a de _cdc_seq=140)
             TOTAL = 150.00                             ✓ CORRETO
```

Agregador incremental só sobre **fato append-only e imutável**. Entidade mutável
agrega por Refreshable MV a partir da view corrente
([ADR-0004 §6](../adr/0004-semantica-cdc-dedup-ordem-delete.md)).

O check `dq.marts.sum_parity` ([ADR-0010](../adr/0010-observabilidade-e-qualidade-de-dados.md))
existe especificamente para pegar esse erro: compara o agregado materializado com
o cálculo independente a partir da view corrente.

### 3.6 Mensagem malformada

```
Connect não consegue converter (schema incompatível, campo obrigatório ausente)
  → mensagem vai para a DLQ (errors.deadletterqueue.topic.name)
  → task continua rodando (errors.tolerance=all)
  → dq.landing.dlq_depth falha → make verify falha
```
DLQ com profundidade > 0 é erro de severidade `error`. Nunca se descarta
silenciosamente.

### 3.7 Replay de offsets

Armadilha operacional do exactly-once: com `exactlyOnce=true`, o estado no
KeeperMap registra quais batches já foram aplicados. **Rebobinar o offset sem
limpar esse estado faz o connector ignorar o replay** — nada é reinserido, e não
há erro.

Procedimento correto em
[`../runbooks/troubleshooting-ingestion.md`](../runbooks/troubleshooting-ingestion.md),
com alvo `make ingestion-reset-state`.

## 4. Latência esperada por salto

Valores-alvo da PoC, a serem **medidos** e registrados em
[`../evaluation/`](../evaluation/) — são insumo da cotação do ClickHouse Cloud.

| Salto | Alvo | Governado por |
|---|---|---|
| Kafka → L0 | < 5 s (p95) | batch do connector, `consumer.override.max.poll.records` |
| L0 → L1 | < 100 ms | MV incremental, síncrona ao INSERT |
| L1 → L2 (aplicacional) | ≤ intervalo de refresh (1 min) | Refreshable MV |
| L1 → L2 (analítico) | ≤ 15 min | Refreshable MV |
| L2 → resposta da API | < 50 ms (p95) | `ORDER BY` do mart, projections |
| Consulta pontual em L1 com `FINAL` | < 200 ms (p95) | `ORDER BY`, quantidade de parts |

## 5. Idempotência e garantias, ponta a ponta

| Etapa | Garantia | Como |
|---|---|---|
| Kafka → L0 | exactly-once | KeeperMap state store do connector |
| L0 → L1 | idempotente | reinserir a mesma `_cdc_seq` colapsa no Replacing |
| L1 → L2 | idempotente | Refreshable MV recalcula a janela do zero |
| Reprocesso de L1 | idempotente | `TRUNCATE` + `INSERT ... SELECT` de L0 |
| Replay de Kafka | requer reset de estado | ver §3.7 |

Nenhuma etapa depende de "não ter falhado antes".
