# Fan-in: como N tópicos convergem em um agregado

O [`data-flow.md`](data-flow.md) segue **uma** mensagem da origem ao relatório.
Este doc olha o movimento contrário: como mensagens de **vários** tópicos,
chegando em ordens e velocidades diferentes, convergem num único número — e
por que essa convergência acontece onde acontece.

## 1. O desenho do funil

Até L1 o fluxo é **paralelo e isolado**: cada tópico tem seu próprio
connector, sua própria landing e seu próprio core. Nenhum trilho conhece o
outro. A convergência (fan-in) acontece **só em L2**, e só por dois
mecanismos: Refreshable MV e Dictionary.

```
 8 tópicos            8 sinks             8 landing (L0)        8 core (L1)
 sap.<ctx>.<ent>.v1   Kafka Connect       dh_landing.*_raw      dh_core.<ctx>__<ent>
                                                                + v_*_current
 order ─────────────► sink ─────────────► order_raw ──MV──────► order ────────────┐
 order_item ────────► sink ─────────────► order_item_raw ─MV──► order_item ───────┤
 order_payment ─────► sink ─────────────► order_payment_raw ──► order_payment ────┤
 customer ──────────► sink ─────────────► customer_raw ──MV───► customer ─────────┤ FAN-IN
 business_unit ─────► sink ─────────────► business_unit_raw ──► business_unit ──┐ │ (L2)
 discount_codes ────► sink ─────────────► discount_codes_raw ─► discount_codes ─┤ │
 prices ────────────► sink ─────────────► prices_raw ──MV─────► prices ─────────┤ │
 stock_position ────► sink ─────────────► stock_position_raw ─► stock_position ─┤ │
                                                                                │ │
                                              ┌─────────────────────────────────┘ │
                                              ▼                                   ▼
                                    DICTIONARIES (memória)              REFRESHABLE MVs
                                    dict__business_unit                 mv__revenue_by_bu_day
                                    dict__discount_codes                  (15 min, janela 3d)
                                              │                         mv__payment_funnel_day
                                              │  dictGet O(1)             (5 min, janela 3d)
                                              └───────────────► dh_marts.revenue_by_bu_day
                                                                dh_marts.payment_funnel_day
```

## 2. O que garante a corretude em cada salto

| Salto | Garantia | Mecanismo |
|---|---|---|
| producer → tópico | ordem por chave de negócio | key da mensagem = chave do contrato (`order_id`, `payment_id`...); mesma chave → mesma partição |
| tópico → landing | exactly-once, 1 tópico → 1 tabela | connector com estado no KeeperMap; sem transformação (ingestão burra, [ADR-0002](../adr/0002-ingestao-via-clickhouse-kafka-connect-sink.md)) |
| landing → core | idempotente, tolera fora-de-ordem | MV incremental linha-a-linha (sem JOIN); `ReplacingMergeTree(_cdc_seq)` fica com a **maior versão**, não a última que chegou |
| core → mart | autocorretivo | Refreshable MV recalcula a janela inteira a cada ciclo, lendo `v_*_current` |

O ponto central: **entre L0 e L1 nunca há correlação**. Cada trilho resolve o
seu CDC sozinho. Se `order_item` chegar antes de `order` (normal — connectors
independentes), nenhum trilho trava nem grava errado; o item fica órfão em L2
por no máximo um ciclo de refresh e se resolve sozinho
([data-flow §3.3](data-flow.md)).

## 3. Por que o fan-in NÃO usa MV incremental

A tentação óbvia seria uma MV incremental com JOIN: "quando chegar o pedido,
junta com os itens". Não funciona, e o motivo está na semântica do ClickHouse:
MV incremental é um **trigger de INSERT na tabela do FROM** — ela não dispara
quando o outro lado do JOIN muda, e vê o lado direito como ele estava naquele
instante. CDC fora de ordem gravaria correlação errada **para sempre**
([ADR-0005](../adr/0005-estrategia-de-agregacao-mv-refreshable-dictionary.md)).

Daí a árvore de decisão do fan-in:

| Situação | Mecanismo | No projeto |
|---|---|---|
| correlacionar entidades **mutáveis** | Refreshable MV (recalcula janela) | `revenue_by_bu_day`, `payment_funnel_day` |
| lookup de **cadastro pequeno** | Dictionary em memória (`dictGet`) | `business_unit`, `discount_codes` |
| correlação **temporal** (vigência) | `ASOF JOIN` em query/refresh | preço de tabela na data do pedido (cenário 005) |
| fato **append-only imutável** | MV incremental → agregador | (nenhum caso na PoC ainda) |

## 4. Anatomia de um fan-in real: `revenue_by_bu_day`

Três entidades convergem (código completo em
`internal/contexts/analytics/sql/30-marts/0020__revenue_by_bu_day.sql`):

```
v_sales__order_current ──────────────┐  cabeçalho: receita, status, moeda
                                     │
v_sales__order_item_current ──┐      │
  · agregado por order_id     ├──────┼──► GROUP BY (bu, dia, canal, moeda)
    ANTES do join (senão      │      │         │
    multiplicaria o total     │      │         ▼
    pelo nº de itens)         ┘      │    revenue_by_bu_day
                                     │
dict__business_unit ─────────────────┘  dictGetOrDefault: nome/canal/região da BU;
                                        BU sem cadastro vira '(sem cadastro)' e
                                        _bu_found=0 — achado, não erro
```

Decisões que aparecem no SQL e valem para qualquer mart novo:

1. **Lê `v_*_current`, nunca a tabela core** — a view resolve dedup e delete.
2. **Agrega o lado N antes do join** — item agregado por `order_id` antes de
   encostar no cabeçalho.
3. **Janela de recálculo** (`today() - 3`): a tolerância a *late arrival*.
   Dado mais atrasado que a janela exige refresh manual com janela maior.
4. **`REFRESH EVERY 15 MINUTE APPEND` + `ReplacingMergeTree(_refreshed_at)`**:
   cada ciclo insere a janela recalculada; o Replacing colapsa para a versão
   mais recente; a leitura é por `v_revenue_by_bu_day` (com `FINAL`).
   Refresh que falha no meio não deixa o mart pela metade.
5. **Cancelamento reescreve o passado**: pedido cancelado hoje muda a receita
   de dias atrás — o recálculo por janela absorve isso sem código especial.

O `payment_funnel_day` segue o mesmo padrão com refresh de 5 min — funil de
autorização/captura por adquirente, deduplicando tentativas por `payment_id`
e contando pedidos com `countDistinct(order_id)` (1 pedido : N tentativas).

## 5. O fan-in via dictionary

Cadastros pequenos não entram no funil de MVs: viram **dictionary em
memória**, recarregado do core a cada 5-10 min (`LIFETIME(MIN 300 MAX 600)`):

```
v_organization__business_unit_current ──► dict__business_unit  (COMPLEX_KEY_HASHED)
v_pricing__discount_codes_current ──────► dict__discount_codes (COMPLEX_KEY_HASHED)
```

`dictGet` no meio do `GROUP BY` custa O(1) e tira o join do plano de execução.
O custo é defasagem de até 10 min — aceitável para cadastro, inaceitável para
transação (por isso `order` nunca vira dictionary). A carga usa o usuário
`dh_dict` (localhost, sem senha) — ver `deploy/clickhouse/users/`.

## 6. Verificação

`make kafka-e2e` percorre o funil inteiro — producer Avro → tópicos →
connectors → landing → core → marts — e roda a matriz de 14 checagens
(`make mock-verify`), que prova exatamente os pontos deste doc: dedup
fora-de-ordem, delete, ressurreição, órfão, ASOF, dictionaries, funil e
receita. Metodologia:
[`../evaluation/spike-mock-flow-clickhouse-kafka.md`](../evaluation/spike-mock-flow-clickhouse-kafka.md).
