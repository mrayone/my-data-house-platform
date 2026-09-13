# Plano de implementação

Documento mestre. Um agente executor **lê este arquivo, depois o arquivo da sua
fase**, e nada mais é necessário para começar.

- Estado atual: [`PROGRESS.md`](PROGRESS.md)
- Regras de trabalho e commit: [`../../CLAUDE.md`](../../CLAUDE.md) §5
- Decisões: [`../adr/README.md`](../adr/README.md)

---

## Objetivo da PoC, em termos de entregável

Ao final da Fase 08, existe:

1. Um ambiente local que sobe com **um comando** e ingere Avro do Kafka no
   ClickHouse com exactly-once.
2. **Oito entidades** plugadas por contrato, sem código específico por entidade.
3. Os **dez cenários** de [`../scenarios/README.md`](../scenarios/README.md)
   produzindo números que fecham com o cálculo independente, sob CDC fora de
   ordem, com delete e com late arrival.
4. Uma **API** que serve o caso aplicacional com latência medida.
5. Um **relatório de avaliação** com números medidos que sustenta a decisão sobre
   contratar o ClickHouse Cloud no GCP.
6. Prova de que plugar a **nona** entidade custa 2 arquivos e 0 linhas de Go.

---

## Fases

| # | Fase | Entrega verificável | Bloqueia | ADRs |
|---|---|---|---|---|
| [00](phases/phase-00-repo-bootstrap.md) | Bootstrap do repositório | `make verify` roda (vazio, mas verde) | 01 | 0009 |
| [01](phases/phase-01-local-environment.md) | Ambiente local + **verificação de paridade** | `make up` e os dois pré-requisitos do Cloud confirmados | 02 | 0002, 0008 |
| [02](phases/phase-02-contracts-and-dhctl.md) | Contratos, codegen e migrations | `make generate` idempotente, `make migrate` do zero | 03 | 0003, 0007 |
| [03](phases/phase-03-provisioning-and-producer.md) | Tópicos, schemas e producer CDC | `make seed` produz Avro nos 8 tópicos | 04 | 0006 |
| [04](phases/phase-04-landing-ingestion.md) | L0 e connectors | dado nas 8 tabelas de landing, sem duplicata, DLQ vazia | 05 | 0002 |
| [05](phases/phase-05-core-layer.md) | L1 core | dedup, delete e fora de ordem provados por e2e | 06 | 0004 |
| [06](phases/phase-06-marts-part-1.md) | Marts — cenários 001-005 | 5 marts com paridade e autocorreção de late arrival | 07 | 0005 |
| [07](phases/phase-07-marts-part-2-and-api.md) | Marts 006-010, API e **extensibilidade medida** | 10 marts, API com p95, 9ª entidade em < 30 min | 08 | 0005, 0011, 0003 |
| [08](phases/phase-08-observability-and-evaluation.md) | Qualidade, benchmark e avaliação | `dhctl dq run` verde, `results.md` preenchido | — | 0010, 0008 |
| [09](phases/phase-09-clickhouse-cloud-validation.md) | **Opcional** — validação no Cloud GCP | mesmos contratos rodando via ClickPipes | — | 0008 |

Dependência é **estritamente sequencial de 00 a 08.** A Fase 09 é opcional e só
faz sentido com 08 concluída.

```
00 ──> 01 ──> 02 ──> 03 ──> 04 ──> 05 ──> 06 ──> 07 ──> 08 ──> (09)
```

### Paralelismo permitido dentro de uma fase

Nas Fases 06 e 07, **cada cenário é independente** e pode ser executado por um
agente diferente, em branches separadas a partir da branch da fase — os arquivos
não se sobrepõem, por desenho do ADR-0009. O que **não** pode ser paralelizado é
a ordem entre fases.

Nas Fases 02 a 05, os contextos (`sales`, `customer`, `pricing`, `inventory`,
`organization`) também são independentes entre si, exceto onde a tarefa disser o
contrário.

---

## Para o agente executor: como uma tarefa funciona

Cada fase contém tarefas com ID `PNN-TX`. Toda tarefa tem, no arquivo da fase:

| Campo | Significado |
|---|---|
| **Objetivo** | uma frase |
| **Arquivos** | todo caminho que a tarefa cria ou altera |
| **Especificação** | o que exatamente construir (sem ambiguidade) |
| **Critérios de aceite** | checklist verificável, com comando e valor esperado |
| **Validação** | o comando exato que prova a tarefa |
| **Commit** | tipo, escopo e mensagem |
| **Docs a atualizar** | além de `PROGRESS.md` |

### Ciclo obrigatório

```
1. ler CLAUDE.md raiz + PROGRESS.md + o arquivo da fase + os ADRs listados
2. confirmar que a fase anterior está DONE em PROGRESS.md   (se não: pare e registre)
3. git checkout main && git pull && git checkout -b feat/phase-NN-<slug>
4. para cada tarefa, na ordem:
     a. implementar
     b. rodar a Validação da tarefa
     c. se falhar: NÃO commitar. Registrar o bloqueio em PROGRESS.md e parar.
     d. se passar: atualizar PROGRESS.md e commitar (tarefa + docs no MESMO commit)
5. ao fim da fase: make verify, marcar a fase DONE em PROGRESS.md, e parar
   para revisão humana — não fazer merge em main
```

### Regras que não se negociam

1. **Critério de aceite que não passa = tarefa não concluída.** Não há
   "praticamente pronto". Registre o bloqueio e pare.
2. **Não invente workaround silencioso.** Se a especificação está errada ou
   impossível, registre em `PROGRESS.md` na seção **Bloqueios**, com o erro
   literal, e pare. Um bloqueio registrado é progresso; um workaround escondido é
   dívida.
3. **Não altere ADR aceito.** Se a implementação exigir decisão diferente,
   escreva um ADR novo (`docs/adr/00NN-*.md`, `Supersedes:`) **antes** do commit
   que a implementa.
4. **Não edite arquivo gerado.** Header `GENERATED by dhctl` significa: mude o
   contrato ou o gerador.
5. **Um commit por tarefa**, incluindo a atualização de `PROGRESS.md`.
6. **Não faça merge em `main`.** A branch da fase fica para revisão.
7. **Se um alvo do `Makefile` não existe, crie-o** em vez de documentar o comando
   cru (`CLAUDE.md` §6).

---

## Alvos do `Makefile` por fase

O `Makefile` cresce junto com o plano. Tabela normativa de quando cada alvo passa
a existir:

| Fase | Alvos introduzidos |
|---|---|
| 00 | `help` `fmt` `lint` `test` `verify` `tools` |
| 01 | `up` `down` `logs` `ps` `reset-env` `parity-check` |
| 02 | `build` `generate` `generate-check` `migrate` `migrate-status` `reset` |
| 03 | `topics` `schemas` `seed` `seed-adverse` |
| 04 | `connectors` `connectors-status` `bootstrap` `ingestion-reset-state` |
| 05 | `core-rebuild` |
| 06 | `mart-refresh` `reports` |
| 07 | `api` `api-smoke` `ext-drill` |
| 08 | `dq` `bench` `evaluation` |

`make verify` acumula: a cada fase, os checks novos entram nele. Ao fim da Fase
08 ele roda: `fmt` · `lint` · `test` · `generate-check` · todos os
`scripts/checks/*.sh` · `dq run --severity error` · smoke e2e.

---

## Checks estáticos (`scripts/checks/`)

Cada um vira erro de build e existe para impedir um erro específico já
identificado nos ADRs.

| Script | Impede | Fase |
|---|---|---|
| `context-boundaries.sh` | `contexts/a` importar `contexts/b`; `platform` importar `contexts` | 00 |
| `no-cross-context-sql.sh` | SQL de um contexto referenciar objeto de outro | 02 |
| `generated-files-clean.sh` | arquivo gerado editado à mão (`dhctl generate --check`) | 02 |
| `contract-ttl.sh` | `landing.ttl_days < 90` | 02 |
| `parity.sh` | engine de arquivo local; DDL sem `{ON_CLUSTER}` | 02 |
| `no-direct-core-read.sh` | L2/L3 lerem `dh_core.<entidade>` sem a view (respeita `-- dq-exception:`) | 05 |
| `no-join-in-incremental-mv.sh` | `JOIN` em `CREATE MATERIALIZED VIEW` sem `REFRESH` | 05 |
| `no-incremental-agg-on-mutable.sh` | MV incremental para engine agregador a partir de L0 de entidade mutável | 05 |
| `grants.sh` | `dh_app` com acesso a `dh_landing` | 05 |
| `money-no-float.sh` | coluna monetária como `Float*` | 05 |

---

## Dataset de benchmark (normativo)

Definido aqui porque **as Fases 03 e 08 dependem dos mesmos números** — e porque
comparar com o ClickHouse Cloud depois exige reproduzir exatamente isto.

| Entidade | Linhas (chaves distintas) | Versões por chave (média) | Mensagens totais |
|---|---|---|---|
| `order` | 5.000.000 | 4 | 20.000.000 |
| `order_item` | 15.000.000 | 2 | 30.000.000 |
| `order_payment` | 6.500.000 | 3 | 19.500.000 |
| `customer` | 2.000.000 | 2 | 4.000.000 |
| `business_unit` | 400 | 2 | 800 |
| `prices` | 1.200.000 | 3 | 3.600.000 |
| `discount_codes` | 5.000 | 2 | 10.000 |
| `stock_position` | 3.000.000 | 20 | 60.000.000 |
| **Total** | — | — | **~137.000.000** |

Parâmetros do gerador (Fase 03), fixos para que o benchmark seja reproduzível:

- Janela de datas: 18 meses até `today()`.
- Catálogo: 400.000 SKUs distintos; 12 centros de distribuição.
- `order_status`: 78% concluídos, 8% cancelados, 14% em trânsito de estado.
- Pagamento: 12% de negação, 6% de retentativa, 2% de estorno.
- Cupom: 22% dos pedidos com cupom; **3% deles com código sem cadastro**.
- Preço: 2% dos SKUs vendidos **sem** preço vigente; 5% das vigências publicadas
  com efeito retroativo.
- Estoque: 4% dos SKUs vendidos **sem** posição; 1% com `available` negativo.
- **Adversidade (obrigatória):** 5% das mensagens fora de ordem por chave, 1,5%
  de `_op='d'`, 0,3% de ressurreição após delete, e 2% de `order_item`
  publicado **antes** do seu `order`.

As porcentagens adversas não são enfeite: são o que faz os critérios de aceite
das Fases 05 a 08 significarem algo.

---

## Riscos do plano e o que fazer

| Risco | Sinal | Ação | Fase |
|---|---|---|---|
| Refreshable MV indisponível ou limitada no ClickHouse Cloud | `parity-check` falha | **pare a Fase 01 e escale.** Toda a camada de marts depende disso. Fallback: tabela + `INSERT ... SELECT` agendado por `dhctl`, em ADR novo | 01 |
| KeeperMap indisponível no Cloud | `parity-check` falha | ADR novo: `exactlyOnce=false` + dedup por `_cdc_seq` em L1 (que já é idempotente) | 01 |
| Throughput insuficiente com `exactlyOnce=true` | `make bench` abaixo da taxa-alvo | medir com `exactlyOnce=false`, comparar, registrar em ADR novo | 08 |
| Refresh total do `customer_metrics` mais longo que o intervalo | `dq.marts.refresh_duration` em `warn` | particionar recálculo por `cityHash64(customer_id) % N`, em ADR novo | 07 |
| Plugar a 9ª entidade exigir Go novo | `ext-drill` estoura a meta | **é um achado da PoC, não um detalhe**: registre em `results.md` e conserte o gerador | 07 |
| `FINAL` inviabilizar a latência aplicacional | p95 da API acima de 50 ms | revisar `ORDER BY`, adicionar projection, ou mover a leitura para o mart | 07 |
| Custo de storage de L0 + L1 + L2 acima do previsto | `bytes_on_disk` em `data_quality_overview` | revisar TTL e codecs; registrar em `results.md` | 08 |

---

## O que está fora de escopo (para não aparecer como tarefa)

- Cluster multi-nó, replicação e disaster recovery.
- Autenticação/autorização de usuário final na API (roles do ClickHouse sim; OAuth não).
- Conversão de moeda.
- Motor de transformação externo (dbt, Spark, Flink) — registrado como evolução no
  ADR-0001 e no ADR-0005.
- Integração com o SAP ou com o Datasphere reais. O `producer` é a origem da PoC.
- Terraform / IaC de produção.
- Cotação comercial do ClickHouse Cloud. A PoC produz os **insumos** da cotação.
