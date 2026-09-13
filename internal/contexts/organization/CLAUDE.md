# CLAUDE.md — contexto `organization`

## O que é

Estrutura organizacional e canal: as unidades de negócio. Cadastro **pequeno**
(centenas de linhas) que quase nunca muda — o caso canônico de **Dictionary**.

## Entidades

| Entidade | Tópico | Chave | Natureza | Partições |
|---|---|---|---|---|
| `business_unit` | `sap.organization.business_unit.v1` | `bu_code` | cadastro pequeno e estável | 1 |

Contrato em [`../../../contracts/domains/organization/`](../../../contracts/domains/organization/).
`core.expose_as_dictionary` declarado: `COMPLEX_KEY_HASHED`, `LIFETIME(MIN 300 MAX 600)`.

## Relações declaradas

Nenhuma saindo. É alvo de `sales/order.business_unit_code` (`lookup`) e de
`pricing/discount_codes.business_unit_scope` (`lookup`).

## Cenários que consomem

001 · 002 · 003 · 004 — como **dimensão**, via `dh_core.dict__business_unit`.

## Por que este contexto importa mais do que parece

`order.business_unit_code` aponta para `business_unit.bu_code`. **Não há chave
surrogate**: a ligação é o próprio código de negócio. É o exemplo de "tópicos que se
conectam com alguma outra coluna" do escopo da PoC, e o cenário 002 existe para
prová-lo.

Dictionary transforma esse lookup em `O(1)` em memória e **tira o join do plano de
execução** ([ADR-0005](../../../docs/adr/0005-estrategia-de-agregacao-mv-refreshable-dictionary.md) §3).

## Armadilhas específicas

1. **`COMPLEX_KEY_HASHED` exige `tuple(chave)`** em `dictGet`, mesmo com chave de
   uma única coluna. Sem o `tuple`, erro de tipo em execução.
2. **`dictGetOrDefault` com default ambíguo** aparece no painel como se fosse uma BU
   real. Use `'(sem cadastro)'`, nunca `''` nem o próprio código.
3. **`dictHas` é o que responde "existe no cadastro?"** — não `INNER JOIN`, que
   apagaria silenciosamente a linha e com ela o achado.
4. **O valor gravado por `dictGet` é o vigente no refresh**, não no momento do
   pedido. Para relatório gerencial isso é o desejado (renomear uma BU renomeia o
   histórico). Se algum dia o requisito virar "o nome que a BU tinha na data", o
   mecanismo passa a ser `ASOF JOIN`, não dictionary.
5. **`LIFETIME(MIN 300 MAX 600)` significa até 10 min de defasagem.** BU criada
   agora aparece como não cadastrada por alguns minutos. Relatórios de "sem
   cadastro" filtram por idade para não gerar alarme falso.
6. **SLA de frescor é 24 h** (`freshness_sla_minutes: 1440`), não 10 min. Cadastro
   pode ficar dias sem mensagem legitimamente — SLA único para todas as entidades
   faria este alarmar sempre ou o transacional nunca.

## IDs nomeados produzidos

| ID | O que exercita |
|---|---|
| `BU-TEST` | BU isolada para asserções de receita (cenário 002) |
| `BU-01` / `BU-02` | escopo de cupom: cadastrado em `BU-01`, usado em `BU-02` (cenário 004) |

## Regras

- Nada cross-context aqui. O dictionary é a **exceção** documentada: ele é
  compartilhado por desenho, e `no-cross-context-sql.sh` permite referências a
  `dh_core.dict__*`.
- A `SOURCE` do dictionary lê `dh_core.v_organization__business_unit_current`,
  nunca a tabela.
