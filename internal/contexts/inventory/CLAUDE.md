# CLAUDE.md — contexto `inventory`

## O que é

Posição de estoque por item e centro de distribuição. Entidade de **alta taxa de
atualização**: a mesma chave recebe muitas versões por dia.

## Entidades

| Entidade | Tópico | Chave | Natureza | Partições |
|---|---|---|---|---|
| `stock_position` | `sap.inventory.stock_position.v1` | `item_id, dc_id` | mutável, **alta taxa** (~20 versões/chave/dia) | 12 |

Contrato em [`../../../contracts/domains/inventory/`](../../../contracts/domains/inventory/).
`landing.ttl_days: 90` (menor que os outros — o volume por chave é alto).

Na Fase 07 este contexto ganha `stock_movement`, como
[prova de extensibilidade](../../../docs/evaluation/extensibility-drill.md).

## Relações declaradas

| De | Para | `kind` | Liga por | Nota |
|---|---|---|---|---|
| `stock_position` | `pricing/prices` | **`loose`** | `item_id` | só compartilham a coluna; base do cenário 010 |

E é alvo de: `sales/order_item` → `stock_position` (`lookup`, chave composta
`item_id, dc_id`, limite de 2%).

## Cenários que consomem

006 (cobertura vs demanda) · 007 (ruptura) · 010 (catálogo parado).

## A chave composta

`(item_id, dc_id)` é o caso de **chave composta** da PoC: nenhuma das duas colunas
basta. E o lado da demanda é **parcialmente nulo** — `order_item.dc_id` é
`Nullable`, nulo enquanto o pedido não é alocado.

Isso gera três diagnósticos distintos, que os cenários 006 e 007 separam e que
**não devem ser misturados**:

| Situação | Significado | Quem investiga |
|---|---|---|
| `available = 0` | ruptura | operação/compras |
| nenhuma posição para o `item_id` | cadastro faltando ou tópico atrasado | dados mestres |
| posição existe, mas não naquele `dc_id` | item vendido de CD que não o estoca | logística |

## Armadilhas específicas

1. **Alta taxa de atualização torna a leitura sem `FINAL` especialmente errada.**
   Com ~20 versões por chave, `SELECT` sem a view corrente pode devolver uma posição
   de horas atrás como se fosse a atual.
2. **`available` negativo não é zerado.** É inconsistência conhecida da origem
   (declarada no contrato) e gera capital negativo no cenário 010 — que é absurdo e
   portanto **visível**. Zerar esconderia o problema; o check
   `dq.stock.negative_available` o reporta.
3. **`available = 0` ≠ "sem registro de estoque".** Confundir os dois manda o time
   errado investigar. Cenário 007 separa as classes.
4. **Anti-join com `= ''`, nunca `IS NULL`.** Coluna não-`Nullable` do lado ausente
   de um `LEFT`/`FULL OUTER JOIN` vem como **valor padrão do tipo** (`''`, `0`), não
   `NULL`. `WHERE IS NULL` retorna **zero** candidatos, em silêncio — a armadilha
   mais perigosa dos cenários 007, 009 e 010.
5. **Anti-join precisa de janela temporal.** Sem ela, dado atrasado é confundido com
   dado ausente, e a taxa é diluída por todo o histórico.
6. **`FULL OUTER JOIN` no cenário 010.** A relação com `prices` é `loose`: nenhum é
   pai do outro, então nenhum pode ser a base exclusiva. `LEFT` perderia metade dos
   achados, e qual metade dependeria da escolha arbitrária do lado.
7. **Relação `loose` não gera `dq.rel.orphan_rate`.** A ausência é o **produto**,
   não defeito. O gerador de checks lê o `kind` do contrato para decidir isso.
8. **`position_at` é o momento da apuração na origem**, não `_kafka_ts`. Usar o
   segundo mediria a latência da plataforma, não a do estoque.

## IDs nomeados produzidos

| ID | O que exercita |
|---|---|
| `SKU-PARADO` | preço + estoque disponível, zero venda |
| `SKU-SEM-PRECO-COM-ESTOQUE` | estoque sem preço (invendável — o achado mais acionável) |
| `SKU-VENDA-CANCELADA` | venda cancelada não reativa o item |
| `SKU-VENDA-ANTIGA` | venda de 120 dias (fora da janela de 90) |

## Regras

- Nada cross-context aqui. Marts que cruzam com `sales` ou `pricing` vivem em
  `analytics`.
- Toda leitura de L1 por `dh_core.v_inventory__stock_position_current`.
- `unit_cost` é `Decimal(18,4)`. Quantidades são `Int64` — podem ser negativas.
