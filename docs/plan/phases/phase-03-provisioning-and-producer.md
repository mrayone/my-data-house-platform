# Fase 03 — Provisionamento e producer de carga CDC

- **Branch:** `feat/phase-03-provisioning-and-producer`
- **Pré-requisito:** Fase 02 `DONE`
- **Bloqueia:** Fase 04
- **ADRs relevantes:** [0006](../../adr/0006-convencoes-avro-e-schema-registry.md), [0003](../../adr/0003-extensibilidade-contract-first.md)
- **Entrega verificável:** `make seed` produz Avro válido nos 8 tópicos, incluindo os casos adversos

## Objetivo da fase

A PoC não tem o SAP. O `producer` **é** a origem, e a qualidade dele determina se
os critérios de aceite das fases seguintes significam algo. Um gerador que só
produz dado bonito valida um sistema que nunca vai existir.

O dataset e as porcentagens adversas estão fixados em
[`../implementation-plan.md`](../implementation-plan.md) → "Dataset de benchmark".
São normativos: a Fase 08 compara contra eles.

---

## P03-T01 — Cliente de Schema Registry e provisionamento de schemas

**Objetivo:** `dhctl schemas apply` registra os 16 subjects com compatibilidade `BACKWARD`.

**Arquivos:**
- `internal/platform/schemaregistry/client.go`
- `internal/platform/schemaregistry/client_test.go`
- `cmd/dhctl/schemas.go`

**Especificação:**
- Cliente HTTP para o Schema Registry: registrar schema, ler subject, ler e
  **definir** nível de compatibilidade, e testar compatibilidade
  (`POST /compatibility/subjects/<s>/versions/latest`).
- `dhctl schemas apply`: para cada contrato, registra `<topic>-key` e
  `<topic>-value` a partir dos `.avsc` gerados, e **afirma explicitamente**
  `BACKWARD` no subject (não confia no default global).
- `dhctl schemas apply --dry-run`: lista subject, ação (`criar` / `nova versão` /
  `sem mudança`) e compatibilidade.
- `dhctl schemas check-compat [paths...]`: testa o schema local contra a última
  versão registrada **sem** publicar. Exit 1 se incompatível, mostrando o motivo
  devolvido pelo registry.
- Cache do `schema_id` retornado em memória; o `producer` reusa.
- Erros do registry são propagados com o corpo da resposta — mensagem genérica aqui
  custa horas de depuração.

**Critérios de aceite:**
- [ ] `dhctl schemas apply` cria 16 subjects
- [ ] `curl -s localhost:8081/config/sap.sales.order.v1-value | jq -r .compatibilityLevel`
      retorna `BACKWARD`
- [ ] `dhctl schemas apply` de novo reporta `sem mudança` para todos
- [ ] `dhctl schemas check-compat` detecta incompatibilidade ao remover, em teste,
      um campo de um contrato (desfazer)
- [ ] `go test ./internal/platform/schemaregistry/...` passa (com servidor HTTP fake)

**Validação:** `make schemas && curl -s localhost:8081/subjects | jq 'length'  # 16`

**Commit:** `feat(platform): adicionar cliente de Schema Registry e dhctl schemas apply`

**Docs a atualizar:** `PROGRESS.md`

---

## P03-T02 — Provisionamento de tópicos

**Objetivo:** `dhctl topics apply` cria os 16 tópicos com as partições dos contratos.

**Arquivos:**
- `internal/platform/kafkaclient/admin.go`
- `cmd/dhctl/topics.go`

**Especificação:**
- Admin client (use `kafka-go`, `franz-go` ou `confluent-kafka-go` — escolha uma e
  registre em comentário o motivo; prefira uma que não exija `cgo`).
- `dhctl topics apply` lê `deploy/kafka/topics.yaml` (gerado na Fase 02) e cria o
  que falta. **Nunca reduz partições** (impossível no Kafka) — se o tópico existe
  com contagem diferente, **falha** explicando.
- `dhctl topics apply --dry-run` e `dhctl topics list` (nome, partições, configs).
- Idempotente: segunda execução não muda nada.

**Critérios de aceite:**
- [ ] `dhctl topics apply` cria 16 tópicos (8 de dados + 8 DLQ)
- [ ] partições batem com `source.partitions` de cada contrato
- [ ] segunda execução reporta "nada a fazer"
- [ ] alterar `partitions` no contrato para menos faz o apply **falhar** com
      mensagem clara (desfazer)

**Validação:** `make topics && ./bin/dhctl topics list`

**Commit:** `feat(platform): adicionar provisionamento de tópicos Kafka`

**Docs a atualizar:** `PROGRESS.md`

---

## P03-T03 — Producer Avro genérico

**Objetivo:** produzir no wire format do Confluent a partir do contrato, **sem
código por entidade**.

**Arquivos:**
- `internal/platform/kafkaclient/producer.go`
- `internal/platform/schemaregistry/serde.go`
- `internal/platform/kafkaclient/producer_test.go`

**Especificação:**
- Wire format Confluent: `0x00` + `schema_id` (4 bytes big-endian) + payload Avro
  binário (ADR-0006 §1).
- `Serializer` genérico: recebe `map[string]any` + o contrato, e serializa
  conforme o `.avsc` — **um único caminho de código para as 8 entidades**. Se
  alguém precisar de um serializador por entidade, o gerador está incompleto
  (ADR-0003).
- Conversões obrigatórias: `decimal` → `bytes` com escala do contrato;
  `timestamp_micros` → `long` em microssegundos; `date` → dias desde a epoch;
  `nullable` → union com `null`.
- Chave: serializada com o schema `<Entity>Key`, com os campos de `source.key`.
- Produção **em lote**, com `acks=all`, idempotência do producer ligada, e
  particionamento pela chave (garante ordem por chave dentro da partição — o que
  torna a adversidade de "fora de ordem" **deliberada**, não acidental).
- Teste: serializar e desserializar com uma lib Avro independente, comparando os
  valores — em especial `decimal` e `timestamp`.

**Critérios de aceite:**
- [ ] o mesmo código serializa as 8 entidades, sem `switch` por entidade
- [ ] round-trip de `Decimal(18,4)` preserva o valor exato (teste com `1234.5678`)
- [ ] round-trip de `timestamp_micros` preserva microssegundos
- [ ] campo `nullable` nulo produz union `null` e desserializa como nulo
- [ ] os bytes começam com `0x00` seguido do `schema_id` correto
- [ ] `go test ./internal/platform/kafkaclient/...` passa

**Validação:** `go test ./internal/platform/kafkaclient/... ./internal/platform/schemaregistry/...`

**Commit:** `feat(platform): adicionar producer Avro genérico no wire format do Confluent`

**Docs a atualizar:** `PROGRESS.md`

---

## P03-T04 — Geradores de domínio por contexto

**Objetivo:** dado sintético coerente entre entidades, por contexto.

**Arquivos:**
- `internal/contexts/organization/generator/business_unit.go`
- `internal/contexts/customer/generator/customer.go`
- `internal/contexts/pricing/generator/prices.go`
- `internal/contexts/pricing/generator/discount.go`
- `internal/contexts/inventory/generator/stock.go`
- `internal/contexts/sales/generator/order.go`
- `internal/contexts/sales/generator/order_item.go`
- `internal/contexts/sales/generator/payment.go`
- `internal/contexts/*/generator/*_test.go`

**Especificação:**
- Cada gerador expõe `Generate(ctx, params) <-chan Record`, onde `Record` é
  `{Key map[string]any, Value map[string]any}` — **sem tipo específico por
  entidade no caminho de produção**.
- **Semente determinística** (`--seed`): a mesma semente produz exatamente o mesmo
  dataset. Sem isso, benchmark não é comparável e teste e2e é instável.
- **Coerência referencial é responsabilidade do gerador**, e é controlada:
  `order.customer_id` sai do conjunto de clientes gerados, salvo a porcentagem
  deliberada de órfãos.
- Ordem de geração: `business_unit` → `customer` → `prices`,
  `discount_codes`, `stock_position` → `order` → `order_item`, `order_payment`.
- Parâmetros do dataset e porcentagens adversas **exatamente** como em
  `../implementation-plan.md` → "Dataset de benchmark". Os valores são flags com
  esses defaults.
- **Casos nomeados e fixos**, criados sempre, para os critérios de aceite das fases
  seguintes (o gerador os produz com IDs literais):
  `ORD-OOO-1` (v2 antes de v1), `ORD-DEL-1` (delete), `ORD-RES-1` (ressurreição),
  `ORD-ORPHAN` (item sem pedido), `CUST-SEM-PEDIDO`, `CUST-CANCEL-ANTIGO`,
  `CUST-5-STATUS`, `CUST-1-PEDIDO`, `SKU-ASOF` (3 vigências),
  `SKU-SEM-PRECO`, `SKU-PRECO-EXPIRADO`, `SKU-VIGENCIA`, `SKU-DUP`,
  `SKU-PREJUIZO`, `SKU-FX`, `SKU-PARADO`, `SKU-SEM-PRECO-COM-ESTOQUE`,
  `SKU-PRECO-SEM-POSICAO`, `SKU-VENDA-CANCELADA`, `SKU-VENDA-ANTIGA`,
  `CUPOM-FANTASMA`, `CUPOM-EXPIRADO`, `CUPOM-LIMITE`, `PROMO10`/`promo10`/`" PROMO10 "`,
  `ACQ-TEST`, `ACQ-LAT-TEST`, `ACQ-RETRY-TEST`, `BU-TEST`.
- Estes IDs são o **contrato entre o gerador e os testes das fases 05-08**. Mudá-los
  quebra critérios de aceite adiante; se precisar mudar, atualize os docs de
  cenário no mesmo commit.
- Dinheiro sempre coerente: `total_amount = gross - discount + freight` no
  cabeçalho, e a soma dos itens **próxima** (a divergência é deliberada em 0,3% dos
  pedidos, para o check `dq.marts.header_item_parity`).
- `_cdc_seq` **monotônica por chave**, e o "fora de ordem" é obtido **publicando**
  fora de ordem, não gerando `_cdc_seq` fora de ordem.
- **Nenhum dado pessoal real.** `document_hash` e `email_hash` são hashes de valores
  sintéticos.

**Critérios de aceite:**
- [ ] `--seed=42` duas vezes produz datasets idênticos (compare hash do stream)
- [ ] integridade referencial respeitada, salvo as porcentagens declaradas
- [ ] todos os IDs nomeados acima são produzidos
- [ ] `_cdc_seq` é monotônica por chave em 100% dos casos
- [ ] `total_amount = gross - discount + freight` em 100% dos cabeçalhos
- [ ] `go test ./internal/contexts/.../generator/...` passa
- [ ] nenhum CPF, e-mail ou nome real no código ou nos dados

**Validação:** `go test ./internal/contexts/... && ./bin/producer --seed=42 --dry-run --limit=1000 | sha256sum` (duas vezes, mesmo hash)

**Commit:** `feat(sales): adicionar geradores de carga CDC por contexto`

**Docs a atualizar:** `PROGRESS.md`; `CLAUDE.md` de cada contexto (lista dos IDs nomeados que o contexto produz)

---

## P03-T05 — `cmd/producer` e os alvos `seed`

**Objetivo:** `make seed` e `make seed-adverse` funcionando.

**Arquivos:** `cmd/producer/main.go`, `Makefile`, `docs/runbooks/seed-data.md`

**Especificação:**
- Flags: `--seed`, `--scale` (`smoke` | `dev` | `bench`), `--entities`
  (lista, default todas), `--adverse` (bool, default `true`),
  `--rate` (msg/s, `0` = sem limite), `--dry-run`, `--limit`.
- Escalas: `smoke` ≈ 10 mil mensagens (segundos), `dev` ≈ 1 milhão (minutos),
  `bench` = o dataset completo de ~137 milhões.
- Progresso em `stderr` a cada N mensagens: entidade, produzidas, taxa, ETA.
- `--adverse=false` gera o dataset **sem** as anomalias — usado para isolar se um
  número errado vem da adversidade ou do pipeline.
- `make seed` = `--scale=dev --seed=42`; `make seed-adverse` =
  `--scale=smoke --seed=42 --adverse=true --entities=...` focado nos IDs nomeados,
  para rodar rápido em e2e.
- Encerramento limpo em `SIGINT`, com flush do producer — mensagem perdida no
  encerramento viraria "bug de ingestão" na fase seguinte.
- `docs/runbooks/seed-data.md`: escalas e quanto tempo cada uma leva, como
  regenerar do zero, como produzir só uma entidade, e a tabela dos IDs nomeados com
  o que cada um exercita.

**Critérios de aceite:**
- [ ] `make seed` produz nos 8 tópicos sem erro
- [ ] `kafka-console-consumer` (ou `dhctl topics tail`) mostra mensagens nos 8 tópicos
- [ ] `--scale=smoke` termina em menos de 60 s
- [ ] `SIGINT` encerra sem perder mensagem já contabilizada
- [ ] `--dry-run` não produz nada e imprime o resumo
- [ ] o runbook lista os IDs nomeados

**Validação:** `make topics && make schemas && make seed && ./bin/dhctl topics list`

**Commit:** `feat(sales): adicionar cmd/producer e alvos de seed`

**Docs a atualizar:** `PROGRESS.md`; `docs/runbooks/seed-data.md`

---

## Critérios de aceite da fase

- [ ] `dhctl schemas apply` cria 16 subjects com `BACKWARD`
- [ ] `dhctl topics apply` cria 16 tópicos com as partições dos contratos
- [ ] `make seed` produz Avro válido nos 8 tópicos
- [ ] mesma semente ⇒ mesmo dataset
- [ ] todos os IDs nomeados presentes
- [ ] round-trip de `decimal` e `timestamp` exato
- [ ] nenhum dado pessoal real
- [ ] `make verify` passa
- [ ] `PROGRESS.md` com a Fase 03 `DONE`
