# Avaliação do ClickHouse Cloud no GCP

Esta pasta é o **entregável final da PoC**: a evidência que sustenta a decisão de
contratar (ou não) o ClickHouse Cloud no GCP.

| Documento | Conteúdo |
|---|---|
| [`clickhouse-cloud-gcp-criteria.md`](clickhouse-cloud-gcp-criteria.md) | critérios, pré-requisitos bloqueantes, o que muda na migração, alternativas |
| [`results.md`](results.md) | todos os números medidos, com fonte e data |
| [`extensibility-drill.md`](extensibility-drill.md) | o custo real de plugar a nona entidade |

## Leitura dos números

> Preencher ao fim da Fase 08, em cinco linhas: o que os números sustentam e o que
> **não** sustentam.

## Três regras de escrita

1. **Todo número tem fonte** — o comando ou a query que o produziu, e a data.
2. **Onde não houver medição, escreva "não medido".** Nunca uma estimativa
   apresentada como número; extrapolação vem rotulada como extrapolação, com o
   fator usado.
3. **Nenhuma recomendação de compra.** O documento apresenta evidência; a decisão é
   de quem tem o contexto comercial.

## O que a PoC deliberadamente não prova

Registrado aqui desde o início para que a decisão não se apoie em medição
inexistente ([ADR-0008](../adr/0008-topologia-self-hosted-e-paridade-com-clickhouse-cloud.md)):

- **Custo real do Cloud.** É cotação, não medição. A PoC produz os *insumos*:
  volume por camada, razão de compressão, taxa de ingestão sustentada, CPU/RAM no
  pico.
- **Autoscaling** e separação de compute/storage.
- **SLA e disaster recovery** gerenciados.
- **Comportamento em cluster multi-nó.** A PoC é single-node; o placeholder
  `{ON_CLUSTER}` mantém a porta aberta, mas nada foi medido em cluster.

A Fase 09 (opcional) fecha parte disso com um trial.
