# Exercício de extensibilidade — plugar a nona entidade

**Status:** a executar na Fase 07 (P07-T07).

Este é o **experimento central da tese do projeto**. A promessa do
[ADR-0003](../adr/0003-extensibilidade-contract-first.md) é que plugar um tópico
novo custa **2 arquivos escritos à mão e 0 linhas de Go**. Aqui isso é medido.

Se uma meta estourar, **é um achado da PoC, não um detalhe**: significa que o
gerador está incompleto, e é exatamente a diferença entre uma plataforma e um
conjunto de scripts.

---

## Entidade escolhida

`inventory/stock_movement` — movimentações de estoque. Escolhida porque:

- é uma entidade **nova**, não uma variação de outra já existente;
- tem duas relações (`lookup` para `stock_position` e para `prices`);
- tem `decimal` (`unit_cost`) e `timestamp` (`moved_at`), então exercita o
  mapeamento de tipos;
- é de alta taxa de atualização, como `stock_position`.

Contrato-exemplo completo em
[`../architecture/extensibility.md`](../architecture/extensibility.md).

---

## Procedimento cronometrado

1. marcar o tempo inicial
2. escrever `contracts/domains/inventory/stock_movement.yaml`
3. `dhctl contract validate`
4. `make generate`
5. renomear `20-core/0030__stock_movement.sql.tmpl` → `.sql` e completar
6. `make bootstrap`
7. gerar carga **com o gerador genérico**, sem código específico
8. `dhctl dq run --severity error`
9. marcar o tempo final

`scripts/ext-drill.sh` automatiza os passos 3-8 e conta os itens abaixo.

---

## Resultado

| Item | Meta | Medido | Observação |
|---|---|---|---|
| Arquivos escritos à mão | 2 | | |
| Arquivos existentes alterados (fora de docs) | 0 | | |
| Linhas de Go escritas | 0 | | |
| Comandos até dado agregado | 3 | | |
| Tempo de ponta a ponta | < 30 min | | |
| Artefatos gerados automaticamente | 5 | | avsc ×2, DDL L0, connector, tópico |

---

## O que atrapalhou

*Seção obrigatória e honesta.* Cada ponto em que o gerador ficou devendo, cada
decisão que o contrato não conseguia expressar, cada mensagem de erro confusa, cada
passo manual que deveria ser automático.

| # | O que | Onde | Impacto | Tarefa de correção |
|---|---|---|---|---|

Itens não corrigidos nesta fase vão para `docs/TECH-DEBT.md` com referência a esta
tabela.

---

## Conclusão

*A escrever após o exercício:* a promessa do ADR-0003 se sustenta? Em que condições?
O que precisaria mudar para plugar 30 tópicos e não 9?
