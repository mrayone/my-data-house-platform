# CLAUDE.md — docs/

## O que é esta pasta

Toda a documentação do projeto. Não há documentação fora daqui, exceto os
`CLAUDE.md` de pasta (que são mapas locais) e os comentários no código.

## Estrutura e para que serve cada parte

| Pasta | Conteúdo | Quem escreve | Muda depois? |
|---|---|---|---|
| [`architecture/`](architecture/) | visão, camadas, fluxo de dados, extensibilidade | arquitetura | sim, junto com o sistema |
| [`adr/`](adr/) | decisões arquiteturais | quem decide | **não.** ADR aceito é imutável |
| [`scenarios/`](scenarios/) | especificação dos 10 relatórios | quem modela | sim, junto com o SQL |
| [`runbooks/`](runbooks/) | procedimentos operacionais | quem opera | sim, depois de rodar o procedimento |
| [`plan/`](plan/) | plano de implementação, fases, progresso | quem planeja / executa | `PROGRESS.md` a cada tarefa |
| [`evaluation/`](evaluation/) | critérios e números da avaliação do Cloud | quem mede | sim, a cada medição |

## Regras

1. **ADR aceito não é editado.** Decisão mudou? ADR novo com `Supersedes: ADR-000X`,
   e o antigo recebe `Superseded by:` **apenas na linha de status**. Isso vale mesmo
   para corrigir algo que se revelou errado — o histórico da decisão é o valor.
2. **Doc de cenário é especificação, não descrição.** Se o SQL implementado
   divergir do doc, um dos dois está errado; corrija **no mesmo commit**.
3. **Runbook não se escreve antes de rodar o procedimento.** Runbook baseado em
   intenção documenta a intenção.
4. **Todo comando em documentação é alvo do `Makefile` ou subcomando do `dhctl`.**
   Comando cru em doc apodrece na primeira mudança de flag. Se o alvo não existe,
   crie-o (`../CLAUDE.md` §6).
5. **Número em `evaluation/` tem fonte e data.** Sem medição, escreva
   "não medido" — nunca uma estimativa apresentada como número.
6. **Português do Brasil** no texto; identificadores, SQL e nomes de
   tabela/coluna/tópico em inglês `snake_case`.
7. **Link relativo entre docs**, sempre. `make verify` inclui verificação de links
   ao fim da Fase 08.

## Onde escrever o quê

| Você quer registrar... | Vai para |
|---|---|
| uma decisão com alternativas descartadas | `adr/` (ADR novo) |
| como o sistema funciona hoje | `architecture/` |
| o que um relatório deve produzir, e como validar | `scenarios/<nnn>-*.md` |
| como executar uma operação | `runbooks/` |
| o que construir e em que ordem | `plan/phases/` |
| o que já foi construído | `plan/PROGRESS.md` |
| um número medido | `evaluation/results.md` |
| algo que ficou frágil ou de fora | `TECH-DEBT.md` (criado na Fase 08) |

## Ordem de leitura para quem chega

1. [`../CLAUDE.md`](../CLAUDE.md) — o mapa e as regras
2. [`architecture/overview.md`](architecture/overview.md) — o problema e o desenho
3. [`architecture/layered-model.md`](architecture/layered-model.md) — o que cada camada faz e **não** faz
4. [`adr/README.md`](adr/README.md) — as decisões e o que foi descartado
5. [`scenarios/README.md`](scenarios/README.md) — o que a PoC tem de provar
6. [`plan/implementation-plan.md`](plan/implementation-plan.md) — o que construir
