# Estrutura do repositório

Mapa anotado das pastas **como construídas** (spike das Fases 01-04). O mapa
resumido e as regras de trabalho estão em [`../../CLAUDE.md`](../../CLAUDE.md);
este doc detalha o que existe hoje em cada pasta e como os artefatos se
relacionam.

## Árvore anotada

```
my-data-house-platform/
├── CLAUDE.md                  # mapa do repo + contrato de trabalho (leia primeiro)
├── README.md                  # visão executiva + como rodar
├── Makefile                   # TODOS os comandos de desenvolvimento (nunca comando cru)
├── docker-compose.yml         # ambiente local: ClickHouse, Keeper, Kafka (KRaft),
│                              # Schema Registry, Kafka Connect, Kafka UI
├── .tool-versions             # go fixado (asdf)
│
├── contracts/                 # FONTE DA VERDADE da extensibilidade
│   ├── schema.json            # JSON Schema que valida os descritores
│   └── domains/<ctx>/*.yaml   # um descritor por entidade: tópico, partições,
│                              # chave, colunas, relações, TTL
│
├── schemas/avro/<ctx>/*.avsc  # schemas Avro por entidade (spike: manuais;
│                              # definitivo: saída de `dhctl generate`).
│                              # Espelham os DDLs de landing menos _ingested_at
│
├── deploy/                    # configuração de AMBIENTE (sem lógica de domínio)
│   ├── clickhouse/config/     # XML do servidor: logging, keeper, keeper_map,
│   │                          # listen_host (05-listen.xml), features
│   ├── clickhouse/users/      # usuários XML: dh_dict (source de dictionaries)
│   ├── keeper/                # keeper_config.xml (single-node, ADR-0008)
│   └── connect/
│       ├── Dockerfile         # cp-kafka-connect + plugin clickhouse-kafka-connect
│       ├── *.zip              # release do plugin, copiado no build (sem rede)
│       └── connectors/*.json  # um sink por tópico: topic2TableMap, exactlyOnce,
│                              # DLQ própria, senha via placeholder
│
├── db/shared/00-bootstrap/    # SQL compartilhado: criação dos databases dh_*
│
├── internal/                  # código Go (ver internal/CLAUDE.md para as fronteiras)
│   ├── platform/              # kernel: NÃO conhece domínio
│   │   ├── config, logging, version
│   │   ├── kafkaclient/       # Schema Registry client, codec Avro (wire format
│   │   │                      # Confluent), producer síncrono (franz-go)
│   │   ├── mockgen/           # tipos do gerador sintético (Record, Envelope CDC)
│   │   └── mockrun/           # Dataset, escrita NDJSON, publicação Kafka
│   └── contexts/<ctx>/        # CADA CONTEXTO É AUTOCONTIDO
│       ├── generator/         # carga CDC sintética da entidade (IDs nomeados)
│       └── sql/               # SQL por camada, ordem derivada do número:
│           ├── 10-landing/    #   L0: tabelas *_raw (MergeTree append-only)
│           ├── 20-core/       #   L1: ReplacingMergeTree + MV + view v_*_current
│           └── 30-marts/      #   L2: dictionaries e marts (analytics = cross-context)
│
├── cmd/                       # binários FINOS (flag parsing + wiring)
│   ├── producer/              # `mock` (NDJSON) e `kafka` (Avro nos tópicos);
│   │                          # datasets.go = wiring dos generators (ADR-0009)
│   ├── dhctl/                 # codegen/migrations/provisionamento (Fases 02+)
│   └── api/                   # serving de relatórios (Fase 07)
│
├── scripts/                   # shell chamado pelos alvos do Makefile
│   ├── checks/                # wait-healthy, context-boundaries (fronteiras Go)
│   ├── db/apply-ddl.sh        # aplica bootstrap+landing+core+marts na ordem
│   ├── kafka/                 # create-topics.sh (partições dos contratos + DLQs),
│   │                          # apply-connectors.sh (REST 8083, senha do .env)
│   └── mock/                  # load.sh (NDJSON direto) e verify.sh (14 checagens)
│
├── docs/                      # toda a documentação (ver docs/CLAUDE.md)
├── test/                      # e2e e fixtures (Fases 05+)
├── tools/                     # go.mod isolado das ferramentas de dev
├── bin/                       # binários compilados (gitignored)
└── out/mock/                  # NDJSON gerado pelo producer mock (gitignored)
```

## Como os artefatos se encadeiam

Para uma entidade `sales/order`, os artefatos formam uma cadeia — hoje mantida
à mão no spike, na Fase 02 gerada por `dhctl generate` a partir do contrato:

```
contracts/domains/sales/order.yaml          (tópico, partições, chave, colunas)
   │
   ├── schemas/avro/sales/order.avsc        (schema registrado no SR pelo producer)
   ├── tópico sap.sales.order.v1            (make kafka-topics, partições do contrato)
   ├── deploy/connect/connectors/
   │     dh-sink-sales-order.json           (sap.sales.order.v1 → sales__order_raw)
   └── internal/contexts/sales/sql/
         ├── 10-landing/0010__order.sql     (dh_landing.sales__order_raw)
         └── 20-core/...                    (tabela + MV + v_sales__order_current)
```

Divergência entre elos da cadeia é o que o `make verify` da Fase 02 em diante
passa a acusar ([ADR-0003](../adr/0003-extensibilidade-contract-first.md)).

## Onde cada coisa roda

| Comando | O que toca |
|---|---|
| `make up` | docker-compose.yml + deploy/ |
| `make db-apply` | db/shared + internal/contexts/*/sql |
| `make kafka-topics` | contracts (partições) → broker |
| `make connectors-apply` | deploy/connect/connectors → Connect REST |
| `make kafka-load` | cmd/producer + schemas/avro → tópicos |
| `make kafka-e2e` | tudo acima, na ordem, + verificação |
