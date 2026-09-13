# CLAUDE.md — deploy/

## O que fica aqui

Configuração de **ambiente**: como os componentes sobem e se ligam. Não há lógica de
domínio aqui.

```
deploy/
├── clickhouse/
│   ├── config/     <- XML do servidor: logging, keeper, keeper_map, features
│   └── users/      <- perfis e settings por role (ADR-0011 §4)
├── keeper/         <- keeper_config.xml
├── connect/
│   ├── Dockerfile  <- imagem do Connect com o plugin do ClickHouse (versão pinada)
│   └── connectors/ <- GERADO por dhctl: um .json por tópico
├── kafka/
│   └── topics.yaml <- GERADO por dhctl
└── observability/  <- opcional: Prometheus + Grafana (compose separado)
```

## Gerado vs escrito à mão

| Caminho | Origem | Editar à mão? |
|---|---|---|
| `connect/connectors/*.json` | `dhctl generate` a partir dos contratos | **não** |
| `kafka/topics.yaml` | `dhctl generate` | **não** |
| `clickhouse/config/*.xml` | humano | sim |
| `clickhouse/users/*.xml` | humano | sim |
| `keeper/keeper_config.xml` | humano | sim |
| `connect/Dockerfile` | humano | sim |
| `observability/**` | humano | sim |

Arquivo gerado carrega `_generated_from` e `_contract_hash`. `make verify` falha se
divergir do contrato ([ADR-0003](../docs/adr/0003-extensibilidade-contract-first.md)).

## Regras

1. **Nenhum segredo versionado.** Config de connector usa `${env:...}`; os valores
   vêm de `.env`, que está no `.gitignore`. Só `.env.example` é versionado, sem
   valores reais.
2. **Versões fixadas por tag e digest** em `docker-compose.yml` e no `Dockerfile`
   do Connect, e registradas em
   [`../docs/evaluation/clickhouse-cloud-gcp-criteria.md`](../docs/evaluation/clickhouse-cloud-gcp-criteria.md).
   Sem versão fixa, a comparação com o Cloud perde sentido
   ([ADR-0008](../docs/adr/0008-topologia-self-hosted-e-paridade-com-clickhouse-cloud.md) §6).
3. **`keeper_map_path_prefix` é requisito**, não detalhe: é o state store do
   exactly-once do connector
   ([ADR-0002](../docs/adr/0002-ingestao-via-clickhouse-kafka-connect-sink.md)).
4. **`exactlyOnce=true` é incompatível com buffering interno** do connector. Não
   adicione configuração de buffer.
5. **DLQ obrigatória** em todo connector (`errors.deadletterqueue.topic.name`).
   Mensagem descartada em silêncio é inaceitável.
6. **`auto.create.topics.enable=false`** no Kafka. Tópico é criado por
   `dhctl topics apply`, com as partições do contrato — criação automática
   esconderia contrato faltante.
7. **Sem engine de arquivo local** e sem disco nomeado exótico no ClickHouse:
   quebraria a paridade com o Cloud (`scripts/checks/parity.sh` verifica).
8. **Healthcheck real** em todo serviço. `make up` espera os healthchecks e falha em
   timeout, em vez de retornar verde com serviço subindo.

## Perfis de acesso (ADR-0011 §4)

| Role | Uso | Settings-chave |
|---|---|---|
| `dh_app` | API aplicacional | `max_execution_time=3`, `readonly=1`, `final=1`, `max_result_rows` limitado |
| `dh_analyst` | BI e ad-hoc | `max_execution_time=60`, `max_memory_usage` maior, `readonly=1` |
| `dh_pipeline` | `dhctl` e connector | DDL e `INSERT` nos databases `dh_*` |

`max_execution_time=3` no `dh_app` é intencional: query aplicacional lenta deve
**falhar rápido e virar bug**, não degradar o serviço.
