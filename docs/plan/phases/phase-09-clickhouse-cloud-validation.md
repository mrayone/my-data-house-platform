# Fase 09 — Validação no ClickHouse Cloud no GCP (opcional)

- **Branch:** `feat/phase-09-cloud-validation`
- **Pré-requisito:** Fase 08 `DONE`
- **Status:** **opcional.** Só execute com decisão explícita de abrir trial ou
  contrato no ClickHouse Cloud.
- **ADRs relevantes:** [0008](../../adr/0008-topologia-self-hosted-e-paridade-com-clickhouse-cloud.md), [0002](../../adr/0002-ingestao-via-clickhouse-kafka-connect-sink.md)
- **Entrega verificável:** os mesmos contratos e migrations rodando no Cloud, alimentados por ClickPipes

## Objetivo da fase

Fechar as perguntas que a PoC self-hosted deliberadamente **não** responde
(ADR-0008): comportamento do produto real, ClickPipes no lugar do Connect, e
consumo de recurso com separação de compute e storage.

Toda a razão de esta fase ser curta é o desenho do ADR-0002: como a ingestão não
transforma nada, trocar Connect por ClickPipes **não deveria** exigir mudança em
L0, L1, L2 ou L3. **Esta fase testa exatamente essa hipótese.** Se ela falhar, é o
achado mais importante do projeto.

---

## P09-T01 — Provisionar o serviço e aplicar as migrations

**Objetivo:** o schema da PoC existindo no Cloud, sem reescrita.

**Especificação:**
- Serviço no ClickHouse Cloud, região **GCP** (a mesma dos sistemas consumidores).
- `dhctl migrate` apontando para o Cloud via env (`CLICKHOUSE_DSN`), com
  `CLICKHOUSE_CLUSTER` configurado conforme o Cloud exigir — o placeholder
  `{ON_CLUSTER}` do ADR-0007 existe para este momento.
- **Registre toda migration que precisou ser alterada.** A meta é zero; qualquer
  alteração é um achado e vira linha em `results.md`.

**Critérios de aceite:**
- [ ] os 5 databases e todos os objetos criados no Cloud
- [ ] **número de migrations alteradas registrado** (meta: 0)
- [ ] roles e grants aplicados (`dh_app` sem acesso a `dh_landing`)
- [ ] `make parity-check` adaptado roda contra o Cloud, com os 8 itens

**Commit:** `feat(deploy): aplicar migrations no ClickHouse Cloud`

**Docs a atualizar:** `PROGRESS.md`; `docs/evaluation/results.md`

---

## P09-T02 — ClickPipes no lugar do Kafka Connect

**Objetivo:** trocar a única peça que a migração troca.

**Especificação:**
- Um ClickPipe por tópico, do Confluent Cloud (ou do Kafka da PoC exposto), com
  Schema Registry configurado — os `.avsc` são os **mesmos** gerados pelo `dhctl`.
- Mapear cada pipe para a tabela de landing correspondente.
- **Confirme as limitações documentadas do ClickPipes:** Avro sem referência
  externa de schema (o gerador já garante isso, ADR-0006 §5), registry por HTTPS,
  e o comportamento de erro (`SOURCE_SCHEMA_ERROR`, `DATA_PARSING_ERROR` na tabela
  de erro) — que é o equivalente da DLQ.
- Se possível, automatize via o provider Terraform do ClickHouse
  (`clickhouse_clickpipe`), e **gere a config a partir dos contratos**, como o
  `dhctl` faz para o Connect. Se não for viável nesta fase, registre como dívida.

**Critérios de aceite:**
- [ ] dado chegando nas 8 (ou 9) tabelas de landing no Cloud
- [ ] os mesmos `.avsc` aceitos sem alteração
- [ ] tabela de erro do ClickPipes vazia no caminho feliz, e recebendo a mensagem
      malformada no teste
- [ ] **nenhuma alteração** em L0/L1/L2/L3 foi necessária (ou a lista exata do que
      foi necessário)

**Commit:** `feat(deploy): configurar ClickPipes para ingestão no Cloud`

**Docs a atualizar:** `PROGRESS.md`; `docs/evaluation/results.md`

---

## P09-T03 — Reexecutar a validação e o benchmark

**Objetivo:** comparar maçã com maçã.

**Especificação:**
- `make seed --scale=bench --seed=42` — **a mesma semente** da Fase 08.
- `dhctl dq run --severity error` contra o Cloud: os mesmos checks, os mesmos
  thresholds.
- `make reports` e os 10 testes e2e de cenário.
- `make bench` adaptado: os blocos que fazem sentido no Cloud (ingestão via
  ClickPipes, latência de query, duração de refresh, storage), documentando quais
  não se aplicam.
- Comparativo lado a lado em `results.md`: **self-hosted vs Cloud**, mesma coluna
  de métrica, com a diferença de recursos declarada.

**Critérios de aceite:**
- [ ] `dhctl dq run --severity error` verde no Cloud
- [ ] os 10 cenários passam com os mesmos critérios de aceite
- [ ] tabela comparativa self-hosted vs Cloud em `results.md`
- [ ] **Refreshable MV funcionando no Cloud** — a pergunta mais importante
      (ADR-0005); se não funcionar, ADR novo e replanejamento da camada de marts
- [ ] diferenças de comportamento registradas, inclusive as favoráveis

**Commit:** `test(deploy): validar os 10 cenários no ClickHouse Cloud`

**Docs a atualizar:** `PROGRESS.md`; `docs/evaluation/results.md`

---

## P09-T04 — Fechar a avaliação

**Objetivo:** a coluna `CLOUD` da tabela de pré-requisitos deixa de ter
`A VERIFICAR`.

**Especificação:**
- `docs/evaluation/clickhouse-cloud-gcp-criteria.md`: cada pré-requisito resolvido
  com **evidência de teste próprio**, não de documentação.
- Custo **real** observado no período de trial, com a ressalva de que trial não é
  produção.
- Seção nova: **"O que aprendemos que não sabíamos"** — as diferenças que só
  apareceram no produto real.
- Atualizar a seção "O que a PoC não prova": alguns itens saem dela agora.

**Critérios de aceite:**
- [ ] nenhum `A VERIFICAR` restante na tabela de pré-requisitos
- [ ] custo de trial registrado com a ressalva
- [ ] a seção "O que aprendemos que não sabíamos" existe
- [ ] a avaliação final é legível por quem decide, sem contexto de implementação

**Commit:** `docs(plan): fechar avaliação com validação no ClickHouse Cloud`

**Docs a atualizar:** `PROGRESS.md`; `docs/evaluation/`

---

## Critérios de aceite da fase

- [ ] migrations aplicadas no Cloud, com o número de alterações registrado (meta: 0)
- [ ] ClickPipes ingerindo com os mesmos schemas Avro
- [ ] os 10 cenários passando no Cloud
- [ ] Refreshable MV confirmada no Cloud
- [ ] comparativo self-hosted vs Cloud em `results.md`
- [ ] tabela de pré-requisitos sem `A VERIFICAR`
- [ ] `PROGRESS.md` com a Fase 09 `DONE`
