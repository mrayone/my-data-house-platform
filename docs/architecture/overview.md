# Visão geral da arquitetura

## O problema

O SAP é a fonte de verdade transacional. O Datasphere replica suas tabelas por
CDC para tópicos Confluent em Avro. Hoje, quem precisa cruzar essas informações
— seja uma aplicação de e-commerce pedindo "tudo do pedido X", seja um analista
pedindo "receita por unidade de negócio no mês" — não tem onde fazer isso.

São **~30 tópicos** que se correlacionam de formas diferentes: alguns por ID,
alguns por uma coluna qualquer, alguns só por um campo comum sem entidade
transacional no meio, e alguns não se correlacionam com nada.

A pergunta da PoC é dupla:

1. **O ClickHouse resolve isso sozinho?** Ou seja: dá para consolidar, deduplicar
   e agregar N tópicos CDC usando apenas mecanismos nativos do ClickHouse
   (materialized views, engines de merge, dictionaries), sem introduzir um motor
   de transformação externo?
2. **Vale contratar o ClickHouse Cloud no GCP?** A PoC roda self-hosted para
   produzir a medição que sustenta essa decisão.

## O desenho em uma frase

**Ingestão burra, modelagem rica.** O Kafka Connect Sink joga cada tópico numa
tabela de landing própria, sem transformar nada. Toda a inteligência — dedup de
CDC, resolução de delete, correlação entre entidades, agregação — acontece dentro
do ClickHouse, em camadas.

```
┌──────────┐   CDC    ┌────────────┐  Avro   ┌──────────────┐
│   SAP    │ ───────> │ Datasphere │ ──────> │  Confluent   │
└──────────┘          └────────────┘         │  (N tópicos) │
                                             └───────┬──────┘
                                                     │ AvroConverter
                                                     │ + Schema Registry
                                             ┌───────▼──────────┐
                                             │  Kafka Connect   │
                                             │ ClickHouse Sink  │  exactly-once
                                             │  1 tópico →      │  via KeeperMap
                                             │  1 tabela        │
                                             └───────┬──────────┘
╔════════════════════════════════════════════════════▼═══════════════════════╗
║  ClickHouse                                                                ║
║                                                                            ║
║  L0  dh_landing   append-only, fiel ao Avro, 1 tabela por tópico           ║
║        │  MV incremental (linha a linha, sem JOIN)                         ║
║  L1  dh_core      estado atual por entidade                                ║
║                   ReplacingMergeTree(_cdc_seq, _is_deleted)                ║
║                   + view v_<entidade>_current  ← único ponto de leitura    ║
║        │  Refreshable MV (correlação) · MV incremental (aditivo)           ║
║        │  Dictionary (cadastro pequeno)                                    ║
║  L2  dh_marts     correlações e agregados por assunto                      ║
║        │  VIEW                                                             ║
║  L3  dh_reports   contrato de leitura                                      ║
╚═════════════════════════════════════╤══════════════════════════════════════╝
                                      │
                    ┌─────────────────┴─────────────────┐
                    ▼                                   ▼
            cmd/api (aplicação)                  BI / analista
            GET /orders/{id}/360                 dh_marts, dh_reports
            p95 em ms                            varredura por data
```

Detalhe camada por camada em [`layered-model.md`](layered-model.md).
Percurso de uma mensagem em [`data-flow.md`](data-flow.md).

## As quatro decisões que definem o sistema

| Decisão | Por quê | ADR |
|---|---|---|
| **Ingestão pelo ClickHouse Kafka Connect Sink**, não por consumer Go nem Kafka Engine | é o análogo self-hosted do ClickPipes; exactly-once pronto; valida o produto que vamos contratar, não o nosso código | [0002](../adr/0002-ingestao-via-clickhouse-kafka-connect-sink.md) |
| **Um contrato YAML por entidade gera schema, DDL e connector** | 30 tópicos × 6 artefatos à mão é inconsistência garantida; aqui divergência é erro de CI | [0003](../adr/0003-extensibilidade-contract-first.md) |
| **CDC resolvido na leitura**, com `ReplacingMergeTree(_cdc_seq, _is_deleted)` e views `v_*_current` | corretude não pode depender de o merge ter rodado nem da ordem de chegada | [0004](../adr/0004-semantica-cdc-dedup-ordem-delete.md) |
| **Árvore de decisão fixa entre MV incremental, Refreshable MV, Dictionary e JOIN em query** | MV incremental não é join de streams; usá-la para correlação grava número errado e nunca corrige | [0005](../adr/0005-estrategia-de-agregacao-mv-refreshable-dictionary.md) |

## Onde o Go entra

Go não está no caminho de bytes do Kafka para o ClickHouse — o connector faz
isso melhor. Go é a **plataforma em volta**:

| Binário | Papel | Por que é Go e não SQL/script |
|---|---|---|
| `cmd/dhctl` | lê contratos e gera `.avsc`, DDL de landing e config de connector; registra schemas; cria tópicos; aplica migrations na ordem canônica; roda checks de qualidade | é o motor de extensibilidade: tipagem, validação e codegen determinístico |
| `cmd/producer` | produz carga CDC sintética em Avro nos N tópicos, incluindo cenários adversos (fora de ordem, delete, ressurreição, órfão) | a PoC não tem o SAP; sem isso não há o que validar |
| `cmd/api` | serve os relatórios aplicacionais a partir de L2/L3, com contrato de consulta nomeado | prova o requisito "servir aplicações", com latência medida |

## Contextos

Seis bounded contexts. Contexto é o eixo de organização em **todas** as
dimensões do repositório: contratos, schemas Avro, SQL e código Go
([ADR-0009](../adr/0009-layout-de-pastas-context-first.md)).

| Contexto | Entidades | Papel |
|---|---|---|
| `sales` | `order`, `order_item`, `order_payment` | fatos transacionais |
| `customer` | `customer` | cadastro de cliente |
| `organization` | `business_unit` | estrutura organizacional e canal |
| `pricing` | `prices`, `discount_codes` | preço de tabela e promoções |
| `inventory` | `stock_position` | posição de estoque por CD |
| `analytics` | — | marts que cruzam contextos |

## Como isso responde à pergunta da contratação

A ingestão é a **única** peça que muda ao migrar para o ClickHouse Cloud: Kafka
Connect Sink → ClickPipes. Porque a ingestão não transforma nada, L0, L1, L2 e L3
migram sem reescrita. Contratos, migrations e MVs são os mesmos.

Isso reduz o risco da decisão a duas verificações concretas, feitas antes de
construir sobre elas ([ADR-0008](../adr/0008-topologia-self-hosted-e-paridade-com-clickhouse-cloud.md)):

1. **Refreshable Materialized View** está disponível no ClickHouse Cloud?
2. **KeeperMap** (base do exactly-once) está disponível?

Critérios completos e o que a PoC deliberadamente **não** prova estão em
[`../evaluation/clickhouse-cloud-gcp-criteria.md`](../evaluation/clickhouse-cloud-gcp-criteria.md).

## Os dez cenários de validação

A PoC não é "subiu e ingeriu". Ela é validada por dez relatórios que, juntos,
exercitam todas as formas de correlação do cenário real — inclusive a ausência
de correlação. Índice em [`../scenarios/README.md`](../scenarios/README.md).

## Leitura recomendada, nesta ordem

1. Este documento
2. [`layered-model.md`](layered-model.md) — o que cada camada faz e não faz
3. [`data-flow.md`](data-flow.md) — o caminho de uma mensagem, com os casos difíceis
4. [`extensibility.md`](extensibility.md) — como plugar o tópico 31
5. [`../adr/README.md`](../adr/README.md) — as decisões e o que foi descartado
6. [`../plan/implementation-plan.md`](../plan/implementation-plan.md) — o que construir e em que ordem
