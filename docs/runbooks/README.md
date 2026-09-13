# Runbooks

Procedimentos operacionais. Cada runbook responde "como faço X" com comando
executável, não com descrição.

| Runbook | Para quê | Criado na fase | Status |
|---|---|---|---|
| [`add-new-topic.md`](add-new-topic.md) | plugar um tópico novo — o procedimento que prova a extensibilidade | 02 | **pronto** |
| [`local-environment.md`](local-environment.md) | subir, derrubar e diagnosticar o ambiente local | 01 | a preencher |
| [`bootstrap.md`](bootstrap.md) | a ordem de subida e por que ela é essa | 04 | a preencher |
| [`seed-data.md`](seed-data.md) | escalas de carga e os IDs nomeados de teste | 03 | a preencher |
| [`troubleshooting-ingestion.md`](troubleshooting-ingestion.md) | sintoma → causa → ação na ingestão, incluindo a armadilha do KeeperMap | 04 | a preencher |
| [`reprocess.md`](reprocess.md) | os quatro cenários de reprocesso, e quais dispensam o Kafka | 05 | a preencher |
| [`mart-operations.md`](mart-operations.md) | forçar refresh, diagnosticar refresh travado, corrigir dado fora da janela | 06 | a preencher |
| [`api.md`](api.md) | endpoints, perfis de acesso, o que fazer com `504` | 07 | a preencher |
| [`observability.md`](observability.md) | o que olhar quando está lento, com as queries prontas | 08 | a preencher |
| [`benchmark.md`](benchmark.md) | reproduzir o benchmark do zero | 08 | a preencher |

## Regras para escrever runbook aqui

1. **Comando, não descrição.** "Rode `make mart-refresh MART=order_360`", não
   "atualize o mart".
2. **Todo comando é alvo do `Makefile`** ou subcomando do `dhctl`. Se não é, crie o
   alvo (`CLAUDE.md` §6) — comando cru em documentação apodrece.
3. **Tabela sintoma → causa provável → diagnóstico → ação** em todo runbook de
   operação. O diagnóstico é uma query ou um `curl`, não um palpite.
4. **Diga o que não fazer**, quando houver armadilha. O exemplo canônico é o replay
   de offsets sem limpar o estado do KeeperMap: sem esse aviso, o procedimento
   "correto" falha em silêncio.
