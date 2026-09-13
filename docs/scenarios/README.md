# Cenários de validação

Os dez relatórios que validam a PoC. Juntos, eles exercitam **todas** as formas de
correlação do cenário real — inclusive a ausência de correlação.

A PoC não é considerada válida por "subiu e ingeriu". É válida quando estes dez
cenários produzem números que fecham com o cálculo independente, sob CDC fora de
ordem, com delete e com dado que chega atrasado.

---

## Índice

| # | Título | Perfil | Correlação | Mecanismo | Destino principal | Fase |
|---|---|---|---|---|---|---|
| [001](001-order-360.md) | Visão 360 do pedido | aplicacional | por ID (multi-entidade) | Refreshable MV 1 min + Dictionary | `dh_marts.order_360` | 06 |
| [002](002-revenue-by-bu-day.md) | Receita por BU e dia | analítico | **por coluna não-ID** (`business_unit_code`) | Refreshable MV 15 min + Dictionary | `dh_marts.revenue_by_bu_day` | 06 |
| [003](003-payment-funnel.md) | Funil e aprovação de pagamento | analítico | por ID, **fan-out 1:N** | Refreshable MV 5 min | `dh_marts.payment_funnel_day` | 06 |
| [004](004-discount-effectiveness.md) | Efetividade de cupom | analítico | **por código (string)** + ausência | Dictionary + Refreshable MV 15 min | `dh_marts.discount_effectiveness` | 06 |
| [005](005-real-margin-vs-price-list.md) | Margem real vs preço de tabela | analítico | **temporal / intervalo** | **`ASOF JOIN`** em Refreshable MV 15 min | `dh_marts.item_margin_daily` | 06 |
| [006](006-stock-coverage-vs-demand.md) | Cobertura de estoque vs demanda | analítico | **chave composta** (`item_id`+`dc_id`) | Refreshable MV 15 min | `dh_marts.stock_coverage_dc_item` | 07 |
| [007](007-stockout-anti-join.md) | Ruptura: vendido sem estoque | analítico + operacional | **anti-join** (3 classes) | Refreshable MV 15 min | `dh_marts.stockout_candidates` | 07 |
| [008](008-customer-metrics.md) | Métricas e coorte de cliente | analítico + pontual | por ID, agregação **lifetime** | Refreshable MV 30 min **sem janela** + Projection | `dh_marts.customer_metrics` | 07 |
| [009](009-data-quality-orphans.md) | Orfandade, atraso e integridade | analítico + operacional | **ausência** + metadados de ingestão | Refreshable MV 5 min **gerada** + checks | `dh_marts.data_quality_overview` | 07 |
| [010](010-catalog-without-sales.md) | Catálogo parado | analítico | **`loose`** (só `item_id`) + **`FULL OUTER`** + anti-join | Refreshable MV 60 min + Projection | `dh_marts.idle_catalog_items` | 07 |

---

## Cobertura de capacidades

Cada linha é uma capacidade que o cenário real exige. A coluna "cenário" é a prova.

| Capacidade | Por que importa no cenário real | Cenário que prova |
|---|---|---|
| Join por ID entre entidades mutáveis | o caso mais comum entre os 30 tópicos | 001, 003, 008 |
| Join por coluna que **não** é ID surrogate | o SAP liga por código de negócio, não por chave técnica | 002 |
| Join por **código string** sem integridade referencial | cupom digitado; o cadastro pode não existir | 004 |
| **Chave composta** | posição de estoque é (item, CD); nenhum dos dois basta | 006 |
| Correlação **temporal / por vigência** (`ASOF JOIN`) | preço, câmbio, hierarquia versionada — nenhum lookup resolve | 005 |
| **Anti-join** como produto | "o que não se conecta" é frequentemente a informação | 007, 009, 010 |
| **`FULL OUTER`** entre entidades sem hierarquia (`loose`) | metade dos tópicos não tem pai nem filho | 010 |
| Agregação **aditiva** sobre fato imutável | o caso em que MV incremental é correta e barata | 003 (contagens) |
| Agregação sobre entidade **mutável** | o caso em que MV incremental produz número errado | 002, 008 |
| Agregação **lifetime sem janela** | cancelamento retroativo reescreve o passado | 008 |
| **Dictionary** para cadastro pequeno | tira o join do plano; é a resposta para tópico de cadastro | 002, 004 |
| **Projection** para segundo padrão de acesso | mesma tabela lida por duas chaves, sem duplicar | 008, 010 |
| **Fan-out 1:N** com `countDistinct` correto | pagamento, entrega, movimentação — vários por pedido | 003 |
| Tratamento de **delete** de CDC | o SAP apaga, e o connector não apaga | 001, 008, 010 |
| **Autocorreção de late arrival** | connectors independentes garantem fora de ordem | 001, 009 |
| **Coluna nulável** em agregado e em chave de join | `dc_id`, `authorized_at`, `valid_to` | 003, 006 |
| **Observabilidade do próprio dado** como entregável | sem isso, "funcionou" não é verificável | 009 |
| **SQL gerado a partir de contrato** | 30 tópicos não se documentam à mão | 009 |

### Distribuição por perfil de acesso (ADR-0011)

| Perfil | Cenários | Característica |
|---|---|---|
| Aplicacional (leitura por chave, ms) | 001, 008 (pontual) | `ORDER BY` pela chave de acesso, endpoint nomeado |
| Analítico (varredura por data) | 002, 003, 004, 005, 006, 010 | `ORDER BY` pelo filtro dominante, role `dh_analyst` |
| Operacional (fila de ação) | 007, 009 | consumido por runbook e por `GET /health/data` |

### O que nenhum cenário cobre (limites declarados)

- **Conversão de moeda.** Nenhum mart soma moedas diferentes; exigiria uma tabela
  de câmbio, que não está entre os oito tópicos.
- **Rateio de frete por item.** A origem não fornece a regra.
- **Reconstrução de transições de estado.** O CDC entrega estado, não eventos; o
  cenário 003 explica a consequência.
- **Sazonalidade e curva de vida de produto.** O cenário 010 quantifica capital
  parado, mas não decide descontinuação.
- **Causalidade.** O cenário 004 põe cupom e baseline lado a lado; não afirma que o
  cupom causou o resultado.

Esses limites estão aqui para que a avaliação do ClickHouse Cloud não seja feita
com base em capacidade que a PoC não mediu — a mesma disciplina do
[ADR-0008](../adr/0008-topologia-self-hosted-e-paridade-com-clickhouse-cloud.md).

---

## Anatomia de um doc de cenário

Todo cenário tem as mesmas seções, na mesma ordem:

| Seção | Responde |
|---|---|
| Cabeçalho | perfil, pergunta de negócio, mecanismo, frescor, janela, entidades, tipo de correlação, ADRs |
| Por que este cenário existe na PoC | qual capacidade prova e qual risco endereça |
| Fontes | objeto lido, camada, e **por que aquele objeto e não outro** |
| Modelo de saída | DDL completo, com cada decisão de engine / `ORDER BY` / partição justificada |
| Transformação | SQL completo e executável, comentado onde há decisão não óbvia |
| Regras de negócio | lista numerada, sem ambiguidade: o que conta, o que não conta, como tratar nulo |
| Armadilhas | o que daria número errado, referenciando `CLAUDE.md` §8 |
| Critérios de aceite | checklist com query de validação e **valor esperado explícito** |
| Checks de qualidade associados | `check_id` do catálogo do ADR-0010 |
| Consumo | endpoint ou query de BI, com a forma **correta** de recalcular razões |
| Arquivos no repositório | todo caminho que a implementação cria ou altera |

Duas convenções que valem para todos:

1. **Toda leitura de L1 é pela view `dh_core.v_<contexto>__<entidade>_current`.**
   As únicas exceções estão no cenário 009, anotadas com `-- dq-exception:`, porque
   ali o objeto de medição é o estado físico.
2. **Razões nunca são somadas nem tiradas pela média.** Percentuais e taxas são
   recalculados da razão das somas, tanto no mart quanto no consumo.

---

## Como adicionar um cenário novo

1. **Escolha o número seguinte** (`011`) e o slug. Numeração nunca é reutilizada.
2. **Decida o mecanismo pela árvore do
   [ADR-0005](../adr/0005-estrategia-de-agregacao-mv-refreshable-dictionary.md).**
   Não escolha antes de percorrer a árvore — é o que impede a MV incremental com
   `JOIN`.
3. **Verifique se as entidades necessárias têm contrato.** Se não, o tópico vem
   primeiro: [`../runbooks/add-new-topic.md`](../runbooks/add-new-topic.md).
4. **Se o cenário cruza contextos, ele pertence ao contexto `analytics`**
   ([ADR-0009](../adr/0009-layout-de-pastas-context-first.md)).
5. **Escreva o doc primeiro**, com todas as seções acima — em especial as
   **armadilhas** e os **critérios de aceite com valor esperado**. Doc sem critério
   verificável não é especificação, é intenção.
6. **Declare a janela de recálculo e justifique-a** contra o p99 de idade de órfão
   das relações envolvidas (cenário 009). Janela menor que o atraso real perde dado
   silenciosamente.
7. **Implemente** o SQL nos caminhos declarados na última seção do doc.
8. **Registre os `check_id` novos** em `dh_meta.dq_checks` com threshold e
   severidade.
9. **Escreva o teste e2e** cobrindo, no mínimo: dedup de CDC, delete, late arrival e
   um caso de borda específico do cenário.
10. **Atualize este índice** — as duas tabelas (Índice e Cobertura de capacidades).

### Template

```bash
cp docs/scenarios/_template.md docs/scenarios/011-<slug>.md
```

O template é [`_template.md`](_template.md) nesta pasta: a anatomia acima com as
seções vazias e as duas convenções já escritas.
