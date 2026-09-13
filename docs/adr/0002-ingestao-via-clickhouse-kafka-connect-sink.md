# ADR-0002 — Ingerir via ClickHouse Kafka Connect Sink, não via Kafka Engine nem consumer Go próprio

- **Status:** Accepted
- **Data:** 2026-09-13
- **Decisores:** Engenharia de Dados, Engenharia de Software
- **ADRs relacionados:** 0001, 0003, 0004, 0006, 0008

## Contexto

Existem três caminhos reais para levar Avro do Confluent ao ClickHouse:

1. **Kafka table engine** — o ClickHouse consome o tópico por conta própria.
2. **ClickHouse Kafka Connect Sink** — connector oficial, roda em Kafka Connect;
   disponível também como **connector totalmente gerenciado no Confluent Cloud**.
3. **Consumer próprio em Go** — decodifica Avro e faz `INSERT`.

As forças que decidem:

- **O objetivo da PoC é avaliar o ClickHouse Cloud no GCP.** No ClickHouse Cloud
  o caminho gerenciado equivalente é o **ClickPipes** (ou o connector gerenciado
  do Confluent). Se a PoC validar um consumer Go artesanal, ela valida o consumer
  Go — não o produto que vamos contratar. A PoC precisa ser **isomórfica ao
  destino**.
- **O ambiente de origem é Confluent com Schema Registry e Avro.** O connector
  usa os próprios converters do Confluent (`AvroConverter`), que é exatamente o
  que o Datasphere produz.
- **São ~30 tópicos e a lista vai crescer.** Custo marginal de plugar o tópico
  n+1 tem que ser configuração, não código.
- **Não temos time para operar garantia de entrega artesanal.** O connector
  oficial oferece **exactly-once** usando **KeeperMap** como state store — dentro
  do próprio ClickHouse, sem componente extra.

## Decisão

**A ingestão do caminho principal é o ClickHouse Kafka Connect Sink**, rodando em
Kafka Connect (self-hosted no Compose para a PoC), com:

- `value.converter = io.confluent.connect.avro.AvroConverter` apontando para o
  Schema Registry;
- `exactlyOnce = true` (state store em KeeperMap — requer ClickHouse ≥ 23.3 e
  Keeper disponível);
- **mapeamento estrito 1 tópico → 1 tabela de landing** via `topic2TableMap`;
- **nenhuma transformação no connector** além da SMT `KeyToValue` (para
  materializar a chave Kafka como coluna) e `flatten` quando o CDC entrega
  `before`/`after` aninhados;
- erros de conversão e insert direcionados a **DLQ** (`errors.deadletterqueue.*`).

**Corolário — a ingestão é deliberadamente burra.** Ela não faz join, não faz
agregação, não aplica delete, não deriva coluna. Toda modelagem acontece dentro do
ClickHouse (ADR-0001, ADR-0005). Isso não é limitação contornada: é a fronteira
que torna o sistema extensível, porque o único artefato por tópico novo é um par
(DDL de landing, config de connector) — ambos **gerados** a partir do contrato
(ADR-0003).

**Papel do Go neste projeto** (a stack exige Go, e ele é central — só não no
caminho de bytes):

| Binário | Responsabilidade |
|---|---|
| `cmd/dhctl` | ler contratos e **gerar** `.avsc`, DDL de landing e config de connector; registrar schemas; criar tópicos; aplicar migrations; aplicar connectors via REST do Connect |
| `cmd/producer` | produzir carga CDC sintética em Avro nos N tópicos (imprescindível: não temos o SAP na PoC) |
| `cmd/api` | servir os relatórios/consultas aplicacionais a partir de L2/L3 |

**Escape hatch, não caminho principal:** existe uma porta
`internal/ingestion.Sink` com um segundo adapter, `go-consumer`, para os casos
que o connector comprovadamente não cobre (ex.: tópico cuja chave e valor precisam
ser fundidos com lógica não expressável por SMT). Usar esse adapter exige um ADR
específico justificando o tópico. Ele existe para não travar a PoC, não para ser
a opção confortável.

## Alternativas consideradas

### A) Kafka table engine nativo do ClickHouse
- **Prós:** zero componente extra; `Kafka` engine + MV é o caminho mais curto;
  suporta `AvroConfluent` com `format_avro_schema_registry_url`.
- **Contras:**
  - Autenticação e TLS contra Schema Registry do Confluent Cloud são mais
    limitados e frágeis de configurar do que nos converters do Confluent.
  - Sem exactly-once real; a recuperação de falha é por offset gerenciado pelo
    próprio ClickHouse, com reprocesso e risco de duplicata.
  - Sem DLQ de primeira classe: mensagem malformada bloqueia ou é descartada com
    pouca visibilidade.
  - Acopla o ciclo de vida do consumo ao ciclo de vida do servidor de banco —
    reiniciar ClickHouse para ajustar consumo é operacionalmente ruim.
  - **Não existe no ClickHouse Cloud como caminho recomendado**, então a PoC
    validaria um mecanismo que não vamos usar.
- **Por que não:** falha no critério de isomorfismo com o destino e na garantia
  de entrega. Fica registrado como *fallback* se o Connect se mostrar caro demais
  de operar — nesse caso, novo ADR.

### B) Consumer próprio em Go
- **Prós:** controle total (batching, retry, idempotência, métricas, DLQ
  customizada); nenhuma dependência de plugin; fácil de debugar em Go.
- **Contras:**
  - Reescreve do zero o que o connector oficial já entrega, inclusive
    exactly-once, que é a parte difícil.
  - Cada tópico novo tende a virar código (mesmo com generics, a tentação é
    real), justamente o que o requisito de extensibilidade proíbe.
  - Na migração para ClickHouse Cloud, o consumer continua sendo nosso —
    continuamos operando servidor, escalonamento e observabilidade que o
    ClickPipes entregaria gerenciado.
  - Valida o nosso código, não o produto sob avaliação.
- **Por que não:** custo de manutenção alto para benefício que só aparece nos
  casos de borda — e para esses, o escape hatch acima já basta.

### C) ClickPipes desde já
- **Prós:** é o destino desejado; zero operação.
- **Contras:** **exclusivo do ClickHouse Cloud.** A PoC é self-hosted
  justamente para não precisar contratar antes de decidir.
- **Por que não:** não roda self-hosted. Porém é a razão desta decisão: o
  connector é o análogo self-hosted mais próximo do ClickPipes, então o modelo de
  dados, os contratos e as MVs migram sem reescrita. Ver ADR-0008.

## Consequências

### Positivas
- Exactly-once sem infraestrutura adicional (KeeperMap vive no ClickHouse).
- Avro/Schema Registry pelo caminho canônico do Confluent, incluindo evolução de
  schema.
- Plugar tópico = 1 YAML → `dhctl generate` → `dhctl connectors apply`. Nenhum
  build, nenhum deploy de aplicação.
- Migração para ClickHouse Cloud troca a camada de ingestão por ClickPipes sem
  tocar em L0/L1/L2 — o risco da contratação cai.
- Operação padrão de Kafka Connect: pausar, retomar, rebobinar offset, escalar
  tasks, ler status por REST.

### Negativas / custo aceito
- Um componente a mais no Compose (Kafka Connect + JVM + plugin). Imagem própria
  em `deploy/connect/Dockerfile`.
- **O connector não apaga linha.** Delete de CDC entra como coluna e é resolvido
  na camada core — tratado integralmente no ADR-0004.
- Mapeamento 1:1 tópico→tabela: nada de fan-in na ingestão. É o desenho, mas
  significa mais tabelas de landing.
- `exactlyOnce = true` é **incompatível com o buffering interno** do connector;
  o throughput depende então do batch do consumer Kafka
  (`consumer.override.max.poll.records`) — precisa ser medido em `make bench`.
- Se offsets forem rebobinados manualmente, é obrigatório **limpar as entradas
  correspondentes na tabela de estado do KeeperMap**, senão o connector considera
  o batch já aplicado e o replay não acontece. Vira passo do runbook.

### Riscos e mitigação
| Risco | Mitigação |
|---|---|
| Replay silenciosamente ignorado por estado do KeeperMap | Passo explícito em `docs/runbooks/troubleshooting-ingestion.md` + alvo `make ingestion-reset-state` |
| Mensagem malformada travando a task | DLQ obrigatória em todo connector gerado; alerta sobre profundidade da DLQ (ADR-0010) |
| Divergência entre o que o gerador produz e o que o connector aceita | Teste e2e por contrato na Fase 04 |
| Throughput insuficiente com exactly-once | Medir em `make bench`; se insuficiente, avaliar `exactlyOnce=false` + dedup por `_cdc_seq` em L1 (que já é idempotente por desenho — ADR-0004) e registrar em novo ADR |

## Impacto no repositório

- `deploy/connect/Dockerfile` — imagem do Connect com o plugin do ClickHouse.
- `deploy/connect/connectors/*.json` — **gerados** por `dhctl generate`.
- `internal/codegen/connector/` — gerador da config.
- `internal/ingestion/` — porta `Sink` + adapters `connect` (default) e
  `goconsumer` (escape hatch).
- `internal/platform/schemaregistry/` — cliente usado pelo `dhctl` e pelo
  `producer`.
- KeeperMap exige `<keeper_map_path_prefix>` em `deploy/clickhouse/config/`.

## Como validar

```bash
make bootstrap
curl -s localhost:8083/connectors | jq          # um connector por tópico
curl -s localhost:8083/connectors/<name>/status | jq '.tasks[].state'  # RUNNING
make seed
```

```sql
-- toda tabela de landing recebeu dado
SELECT table, sum(rows) AS rows FROM system.parts
WHERE database='dh_landing' AND active GROUP BY table ORDER BY table;

-- exactly-once ativo: sem duplicata de (_topic,_partition,_offset)
SELECT count() FROM (
  SELECT _topic,_partition,_offset, count() c
  FROM dh_landing.sales__order_raw GROUP BY 1,2,3 HAVING c > 1
);
-- esperado: 0

-- DLQ vazia
```
