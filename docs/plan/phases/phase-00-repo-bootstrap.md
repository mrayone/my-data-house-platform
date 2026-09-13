# Fase 00 — Bootstrap do repositório

- **Branch:** `feat/phase-00-repo-bootstrap`
- **Pré-requisito:** nenhum
- **Bloqueia:** Fase 01
- **ADRs relevantes:** [0009](../../adr/0009-layout-de-pastas-context-first.md)
- **Entrega verificável:** `make verify` executa e passa (sem código de negócio ainda)

## Objetivo da fase

Deixar o repositório pronto para receber código: módulo Go, `Makefile` como
interface única de comandos, lint, teste e os primeiros checks estáticos de
fronteira. Nada de ClickHouse ou Kafka nesta fase.

---

## P00-T01 — Inicializar o módulo Go e a árvore de pacotes

**Objetivo:** módulo Go compilando com a estrutura do ADR-0009.

**Arquivos:**
- `go.mod`, `go.sum`
- `.gitignore`
- `internal/platform/config/config.go`
- `internal/platform/logging/logger.go`
- `cmd/dhctl/main.go`, `cmd/producer/main.go`, `cmd/api/main.go`
- `internal/contexts/<ctx>/doc.go` para os 6 contextos

**Especificação:**
- `module github.com/mrayone/my-data-house-platform`
- Go 1.23 ou superior (use o mais recente estável disponível).
- `internal/platform/logging`: `slog` em JSON, nível por env `LOG_LEVEL`
  (`debug|info|warn|error`, default `info`). Expor
  `logging.New() *slog.Logger`.
- `internal/platform/config`: struct `Config` carregada de env com defaults,
  contendo por enquanto: `ClickHouseDSN`, `KafkaBrokers`,
  `SchemaRegistryURL`, `ConnectURL`, `LogLevel`. Função
  `config.Load() (Config, error)` que **falha com mensagem clara** se uma var
  obrigatória estiver ausente. Sem valores sensíveis no código.
- Os três `main.go` só imprimem nome e versão por enquanto
  (`dhctl version` → `dhctl dev`). Wiring de verdade vem nas fases seguintes.
- `doc.go` de cada contexto: comentário de pacote de uma linha dizendo o que o
  contexto contém (serve para `go doc` e para o check de fronteira encontrar os
  pacotes).
- `.gitignore`: binários (`/bin/`), `.env`, `*.log`, artefatos de cobertura.

**Critérios de aceite:**
- [ ] `go build ./...` compila sem erro
- [ ] `go vet ./...` sem achados
- [ ] `LOG_LEVEL=debug go run ./cmd/dhctl version` imprime JSON estruturado
- [ ] `config.Load()` com env vazio retorna erro nomeando a variável faltante

**Validação:** `go build ./... && go vet ./... && go run ./cmd/dhctl version`

**Commit:** `chore(platform): inicializar módulo Go e árvore de pacotes por contexto`

**Docs a atualizar:** `PROGRESS.md`

---

## P00-T02 — `Makefile` como interface única

**Objetivo:** todo comando de desenvolvimento tem um alvo.

**Arquivos:** `Makefile`, `.env.example`

**Especificação:**
- Alvos desta fase: `help` `tools` `fmt` `lint` `test` `build` `verify`
- `help` é o alvo **default** e lista os alvos com descrição, lida dos comentários
  `## ` ao lado de cada alvo.
- `tools` instala as ferramentas de dev em `./bin` (nunca global):
  `golangci-lint`, `gofumpt`. Use `go install` com versão **pinada** em
  `tools/tools.go` ou `tools/go.mod`.
- `fmt` = `gofumpt -l -w .`; `lint` = `golangci-lint run`;
  `test` = `go test ./... -race -count=1`.
- `verify` desta fase = `fmt-check` (falha se houver diff) + `lint` + `test` +
  `scripts/checks/context-boundaries.sh`.
- `.env.example` com **todas** as variáveis que `config.Load()` lê, comentadas, e
  **nenhum segredo real**. `.env` está no `.gitignore`.
- O `Makefile` carrega `.env` se existir (`-include .env` + `export`).

**Critérios de aceite:**
- [ ] `make` (sem argumento) mostra a ajuda com todos os alvos
- [ ] `make tools` instala em `./bin` e não altera o GOPATH global
- [ ] `make verify` passa em repositório limpo
- [ ] `make verify` **falha** se um arquivo Go estiver mal formatado (teste
      sabotando um arquivo e desfazendo)
- [ ] toda variável lida por `config.Load()` aparece em `.env.example`

**Validação:** `make verify`

**Commit:** `build: adicionar Makefile como interface única de comandos`

**Docs a atualizar:** `PROGRESS.md`; `README.md` (seção "Como rodar")

---

## P00-T03 — Checks de fronteira de contexto

**Objetivo:** a regra 4/5 do ADR-0009 deixa de depender de revisão humana.

**Arquivos:** `scripts/checks/context-boundaries.sh`, `scripts/checks/README.md`

**Especificação:**
- `context-boundaries.sh` falha (exit 1) e imprime o arquivo e a linha se:
  1. um pacote sob `internal/contexts/<a>/` importar
     `internal/contexts/<b>/` com `<a> != <b>`;
  2. um pacote sob `internal/platform/` importar `internal/contexts/`.
- Implementação: `go list -deps -json ./internal/...` ou `grep` sobre os imports
  — prefira `go list`, que não dá falso positivo em comentário.
- Saída em caso de falha precisa dizer **qual regra** foi violada e **como
  corrigir** (mover para `analytics` / para `platform`).
- `scripts/checks/README.md`: tabela com script, o que impede, ADR de origem, e
  em que fase foi introduzido. Todo check novo entra nessa tabela.
- Todo script de check: `#!/usr/bin/env bash`, `set -euo pipefail`, executável.

**Critérios de aceite:**
- [ ] `scripts/checks/context-boundaries.sh` sai 0 no repositório atual
- [ ] sai 1 com mensagem útil quando se adiciona, em teste, um import de
      `contexts/sales` dentro de `contexts/customer` (desfazer depois)
- [ ] sai 1 quando `platform` importa `contexts` (mesmo teste, desfazer)
- [ ] `scripts/checks/README.md` lista o check

**Validação:** `scripts/checks/context-boundaries.sh && make verify`

**Commit:** `test(platform): adicionar check estático de fronteira entre contextos`

**Docs a atualizar:** `PROGRESS.md`; `scripts/checks/README.md`

---

## P00-T04 — CI e `CLAUDE.md` por contexto

**Objetivo:** o que roda local roda no CI; e cada contexto se apresenta.

**Arquivos:**
- `.github/workflows/ci.yml`
- `internal/CLAUDE.md`, `cmd/CLAUDE.md`, `db/CLAUDE.md`, `deploy/CLAUDE.md`
- `internal/contexts/<ctx>/CLAUDE.md` para os 6 contextos

**Especificação:**
- CI em `push` e `pull_request`: `make tools`, `make verify`. Cache de módulos Go.
  **O CI não duplica comandos** — chama alvos do `Makefile`, e só eles.
- `internal/CLAUDE.md`: regra de dependência (`contexts → platform`, nunca o
  contrário, nunca entre contextos), o que vive em `platform` e o que vive em
  `contexts`, e onde ficam os geradores.
- `cmd/CLAUDE.md`: os três binários, o que cada um faz, e a regra "`cmd` é fino —
  só flag parsing e wiring".
- `db/CLAUDE.md`: o que fica em `db/shared/` e por que o SQL de contexto **não**
  fica aqui (ADR-0009), mais a ordem canônica de execução (ADR-0007).
- `deploy/CLAUDE.md`: o que é config de ambiente vs. o que é gerado por `dhctl`.
- `internal/contexts/<ctx>/CLAUDE.md`, para cada contexto: entidades, tópicos,
  natureza (mutável / cadastro / alta taxa), relações declaradas, cenários que o
  consomem, e a regra "nada cross-context aqui — vai para `analytics`".

**Critérios de aceite:**
- [ ] CI verde no push da branch
- [ ] CI não contém comando que não exista como alvo do `Makefile`
- [ ] existe `CLAUDE.md` em `internal/`, `cmd/`, `db/`, `deploy/` e nos 6 contextos
- [ ] cada `CLAUDE.md` de contexto lista as entidades que batem com
      `contracts/domains/<ctx>/`

**Validação:**
```bash
for d in internal cmd db deploy internal/contexts/*/; do test -f "$d/CLAUDE.md" || { echo "falta $d/CLAUDE.md"; exit 1; }; done
```

**Commit:** `docs: adicionar CI e CLAUDE.md por contexto`

**Docs a atualizar:** `PROGRESS.md`

---

## Critérios de aceite da fase

- [ ] `make verify` passa
- [ ] CI verde
- [ ] `go build ./...` compila
- [ ] `CLAUDE.md` presente em `internal/`, `cmd/`, `db/`, `deploy/` e nos 6 contextos
- [ ] `.env.example` cobre todas as variáveis de `config.Load()`
- [ ] nenhum segredo versionado
- [ ] `PROGRESS.md` com a Fase 00 `DONE`
