# ADR-0001 — Adotar modelo em camadas L0→L3 dentro do ClickHouse

- **Status:** Accepted
- **Data:** 2026-09-13
- **Decisores:** Engenharia de Dados, Engenharia de Software
- **ADRs relacionados:** 0004, 0005, 0007, 0011

## Contexto

A origem é um CDC de SAP replicado pelo Datasphere para tópicos Confluent em
Avro. Consequências práticas dessa origem:

- O payload chega **no formato do SAP**, não no formato que a aplicação quer.
- Cada tópico tem seu próprio ritmo. Tabelas de cadastro (`business_unit`,
  `prices`) podem ficar longos períodos sem mensagem; tabelas transacionais
  (`order`, `order_item`) são contínuas.
- Eventos **chegam fora de ordem** entre tópicos e, em cenários de rebalance,
  dentro do mesmo tópico.
- O mesmo dado precisa servir **aplicação** (busca por chave, latência baixa) e
  **análise** (agregação sobre volume).

Se transformarmos durante a ingestão, qualquer erro de modelagem exige reprocessar
Kafka — que tem retenção finita. Se não separarmos serving de bruto, cada mudança
de relatório vira risco de perda de dado.

## Decisão

Adotamos **quatro camadas, todas dentro do ClickHouse**, em databases separados:

| Camada | Database | Conteúdo | Engine típico | Mutável? |
|---|---|---|---|---|
| **L0 — Landing** | `dh_landing` | 1 tabela por tópico, fiel ao Avro, append-only, com metadados Kafka | `MergeTree` com TTL | Não — só cresce |
| **L1 — Core** | `dh_core` | estado atual por entidade, chave de negócio, tipos normalizados | `ReplacingMergeTree(_cdc_seq, _is_deleted)` | Sim (dedup por versão) |
| **L2 — Marts** | `dh_marts` | correlações e agregados por assunto | `AggregatingMergeTree` / `SummingMergeTree` / Refreshable MV | Recalculável |
| **L3 — Reports** | `dh_reports` | views nomeadas e parametrizadas, contrato de leitura | `VIEW` / `Parameterized VIEW` | Não armazena |

Regras que sustentam o modelo:

1. **L0 é a fonte de verdade reprocessável.** Retenção de L0 (TTL) é maior que a
   retenção do Kafka. Reprocessar L1/L2 lê de L0, nunca do Kafka.
2. **Nada consulta L0 diretamente em produção.** L0 é para reprocesso e debug.
3. **A ingestão escreve só em L0.** Ver ADR-0002.
4. **L0→L1 é por Materialized View incremental** (transformação de linha única,
   sem join). Ver ADR-0005.
5. **L1→L2 é por MV incremental quando aditivo e por Refreshable MV quando
   exige correlação.** Ver ADR-0005.
6. **L3 nunca contém lógica de negócio nova** — só recorte, filtro e
   apresentação sobre L2.
7. **Consumidor externo (aplicação, BI) só lê L2 e L3.** Isso permite
   reescrever L1 sem quebrar contrato.

## Alternativas consideradas

### A) Uma única camada: tabela final escrita direto pela ingestão
- **Prós:** menos objetos, menor latência, menos storage.
- **Contras:** qualquer erro de modelagem é irreversível depois da retenção do
  Kafka; impossível mudar agregação sem reprocessar a origem; CDC fora de ordem
  sem camada de dedup gera estado errado permanente.
- **Por que não:** a PoC precisa iterar em modelagem várias vezes. Sem L0,
  cada iteração exige replay do Datasphere, que não controlamos.

### B) Duas camadas (raw → final)
- **Prós:** mais simples que quatro.
- **Contras:** mistura "estado atual da entidade" com "agregado de assunto" na
  mesma tabela. Como a primeira é mutável e a segunda é aditiva (ADR-0004,
  armadilha 4), juntá-las produz dupla contagem em UPDATE de CDC.
- **Por que não:** o problema de corretude é estrutural, não de volume.

### C) Transformar fora do ClickHouse (dbt, Spark, Flink)
- **Prós:** ferramental maduro de transformação, testes e lineage.
- **Contras:** adiciona um runtime, um scheduler e um custo a avaliar junto com
  o ClickHouse Cloud — exatamente o que a PoC quer isolar.
- **Por que não:** a pergunta da PoC é "o ClickHouse dá conta da agregação por
  si só?". Introduzir motor externo responde outra pergunta. Fica como caminho
  de evolução, registrado em `docs/evaluation/`.

## Consequências

### Positivas
- Reprocesso de L1/L2 é um `TRUNCATE` + `INSERT ... SELECT` de L0: minutos, sem
  depender do SAP nem da retenção do Kafka.
- Cada camada tem uma única responsabilidade, então a origem de um número errado
  é localizável por camada.
- Mudar relatório não toca ingestão.

### Negativas / custo aceito
- Storage duplicado (L0 + L1 + L2). Mitigado por `ZSTD` em L0 e TTL.
- Mais objetos para versionar e migrar.
- Latência L0→L2 é a soma dos saltos; para o caso aplicacional, medir e
  registrar em `docs/evaluation/`.

### Riscos e mitigação
| Risco | Mitigação |
|---|---|
| TTL de L0 curto demais inviabiliza reprocesso | TTL de L0 definido por contrato, mínimo 90 dias; checagem em `make verify` |
| Alguém consultar L0 em produção | Grants por database (ADR-0011); L0 não exposto na API |
| Divergência silenciosa entre L1 e L0 | Checks de reconciliação de contagem por chave (ADR-0010) |

## Impacto no repositório

- `db/shared/00-bootstrap/` cria os quatro databases.
- SQL por camada em `internal/contexts/<ctx>/sql/{10-landing,20-core,30-marts,40-reports}/`.
- DDL de L0 é **gerado** a partir do contrato (ADR-0003); L1/L2/L3 são escritos à mão.

## Como validar

```sql
SELECT name FROM system.databases WHERE name LIKE 'dh_%' ORDER BY name;
-- esperado: dh_core, dh_landing, dh_marts, dh_reports

-- nenhuma tabela de landing referenciada por objeto de dh_reports
SELECT count() FROM system.tables
WHERE database = 'dh_reports' AND create_table_query ILIKE '%dh_landing%';
-- esperado: 0
```
