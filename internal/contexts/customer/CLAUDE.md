# CLAUDE.md — contexto `customer`

## O que é

Cadastro de clientes replicado do SAP via Datasphere. Cadastro de **porte médio**
(milhões de linhas no cenário real), mutável, com dado pessoal hasheado na origem.

## Entidades

| Entidade | Tópico | Chave | Natureza | Partições |
|---|---|---|---|---|
| `customer` | `sap.customer.customer.v1` | `customer_id` | mutável; `segment` e `loyalty_tier` mudam | 6 |

Contrato em [`../../../contracts/domains/customer/`](../../../contracts/domains/customer/).
`extensions.pii: true`.

## Relações declaradas

Nenhuma saindo. É o alvo de `sales/order.customer_id` (`foreign_key`).

## Cenários que consomem

001 (360 do pedido) · 008 (métricas e coorte de cliente).

## Por que `customer` NÃO é dictionary

A regra do [ADR-0005](../../../docs/adr/0005-estrategia-de-agregacao-mv-refreshable-dictionary.md) §3
é cadastro **pequeno e estável**. `customer` é nem um nem outro: milhões de linhas
pressionariam a memória do nó, e no cenário 008 o cliente é o **grão**, não uma
dimensão de lookup. Nos marts, entra por `LEFT JOIN` dentro de Refreshable MV.

## Armadilhas específicas

1. **O `FROM` dos marts de cliente é o cliente, com `LEFT JOIN` nos pedidos.**
   Começar pelo pedido perde quem nunca comprou — e o denominador da retenção de
   coorte é o tamanho da coorte, não o número de compradores.
2. **`customer_since` pode ser posterior à primeira compra.** Não se corrige: é
   achado (`dq.customer.signup_after_first_order`) e revela ordem de cadastro
   invertida na origem. `days_to_first_order` fica negativo, de propósito.
3. **Cliente apagado no SAP** desaparece de `v_customer__customer_current`, e seus
   pedidos passam a ser órfãos — contabilizados no cenário 009, não silenciados.
4. **`recency_days` nulo, nunca zero**, para quem nunca comprou. Zero diria
   "comprou hoje", o oposto do fato.
5. **Coorte por `customer_since`**, não por primeira compra. São métricas
   diferentes; a segunda seria um mart novo.

## PII

- `document_hash` e `email_hash` vêm **hasheados da origem**. Nunca em claro.
- **Nenhum dado pessoal em log**, nem em `debug`.
- O gerador produz dado sintético; a regra vale mesmo assim, para não criar hábito
  errado.

## IDs nomeados produzidos

| ID | O que exercita |
|---|---|
| `CUST-SEM-PEDIDO` | cliente sem nenhum pedido (base da coorte) |
| `CUST-CANCEL-ANTIGO` | cancelamento retroativo (prova a ausência de janela) |
| `CUST-5-STATUS` | dedup de pedido com 5 transições |
| `CUST-1-PEDIDO` | comprador de uma única vez (`avg_days_between_orders` nulo) |
| `CUST-000001` | cliente de exemplo do critério de latência do cenário 008 (ID gerado em massa) |

## Regras

- Nada cross-context aqui. Marts que cruzam com `sales` vivem em `analytics`.
- Toda leitura de L1 por `dh_core.v_customer__customer_current`.
