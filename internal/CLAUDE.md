# CLAUDE.md — internal/

## Regra de dependência (enforçada por check)

```
cmd/*  ──>  internal/contexts/*  ──>  internal/platform/*
                    │
                    └──X──>  internal/contexts/<outro>     PROIBIDO
```

| Regra | Por quê | Check |
|---|---|---|
| `contexts/a` **não** importa `contexts/b` | contexto é fronteira; cruzar contextos é papel do contexto `analytics` | `scripts/checks/context-boundaries.sh` |
| `platform` **não** importa `contexts` | o kernel não conhece domínio | idem |
| `cmd` é fino | só flag parsing e wiring; lógica mora aqui | revisão |

Precisou cruzar contextos? Vai para `contexts/analytics/`. É exatamente o que esse
contexto existe para ser ([ADR-0009](../docs/adr/0009-layout-de-pastas-context-first.md)).

## O que vive onde

| Pasta | Conteúdo | Conhece domínio? |
|---|---|---|
| `platform/config` | carga de configuração por env | não |
| `platform/logging` | `slog` em JSON | não |
| `platform/clickhouseclient` | conexão e execução no ClickHouse | não |
| `platform/kafkaclient` | admin, producer, serialização Avro genérica | não |
| `platform/schemaregistry` | cliente do Schema Registry | não |
| `platform/observability` | coleta de métricas e runner de checks de qualidade | não |
| `contract` | parse e validação de descritores | sim (meta) |
| `codegen/avro`, `codegen/ddl`, `codegen/connector` | geradores | sim (meta) |
| `migrate` | runner de migrations e rebuild de L1 | não |
| `reporting/query` | registro e execução de queries nomeadas | não |
| `reporting/http` | handlers da API | não |
| `contexts/<ctx>/generator` | geração de carga CDC daquele contexto | sim |
| `contexts/<ctx>/reports` | queries dos cenários daquele contexto | sim |
| `contexts/<ctx>/sql/` | SQL por camada (`10-landing` … `40-reports`) | sim |

## Por que SQL vive dentro de uma árvore Go

`internal/contexts/<ctx>/sql/` guarda SQL numa pasta de código Go, o que estranha à
primeira vista. É deliberado por duas razões:

1. **Coesão do contexto vence a convenção de linguagem** — tudo de `sales` fica
   junto, e adicionar contexto é criar uma pasta, sem renumerar nada.
2. **`go:embed`** dessas pastas permite que o `dhctl` seja um binário único, sem
   depender do repositório em disco para migrar.

A ordem de execução é derivada do número da camada, não da localização
([ADR-0007](../docs/adr/0007-migrations-sql-versionado.md)).

## Regras de código

1. **Nenhum tópico novo justifica código Go novo.** Se você está escrevendo um
   serializador, um gerador ou um handler por entidade, o gerador está incompleto —
   é um achado, não uma tarefa ([ADR-0003](../docs/adr/0003-extensibilidade-contract-first.md)).
2. **Erro sempre com contexto**: `fmt.Errorf("... %s: %w", algo, err)`. Erro
   genérico em codegen custa horas.
3. **Validação acumula e reporta tudo de uma vez.** Parar no primeiro erro faz o
   usuário rodar oito vezes.
4. **Nada de segredo em log**, nem em `debug`. Nenhum dado pessoal em log.
5. **`context.Context` propagado** até a query do ClickHouse, com timeout.
6. **Saída de gerador é determinística**: ordenação estável e formatação fixa, para
   que `git diff` na segunda execução seja vazio.
7. **Teste *golden* para todo gerador.** É o que impede regressão silenciosa de
   formatação.
