# Dívidas técnicas

Registro de decisões aceitas como dívida — divergências de spec que não
justificam parar a execução, mas que precisam de dono e de critério de saída.
Toda entrada aqui deve ter um bloqueio correspondente em
[`docs/plan/PROGRESS.md`](plan/PROGRESS.md#bloqueios).

| # | Fase/Tarefa | Descrição | Critério de saída | Status |
|---|---|---|---|---|
| TD-001 | P00-T02 | `golangci-lint` não pôde ser instalado no ambiente de execução desta sessão: sua árvore de dependências usa vanity imports (`golang.org/x/*`, `honnef.co/go/tools`, `go-simpler.org/*`, `go.uber.org/*`) que a rede sandboxed não resolve (só `github.com` e alguns registries de pacote estão liberados). `make lint` cai para `go vet ./...` quando o binário não está em `./bin`. `gofumpt` foi viável via `replace` direto para espelhos em `github.com/golang/*` e está fixado em `tools/go.mod`. | Em uma máquina com rede irrestrita: `cd tools && go get github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.1.6 && make tools` deve produzir `./bin/golangci-lint`; a partir daí `make lint` usa o binário automaticamente (o Makefile já detecta a presença dele). | ABERTO |

## Como uma entrada sai desta tabela

Atualize o status para `RESOLVIDO` com o commit que resolveu, ou remova a linha
se a causa raiz deixou de existir (ex.: rede do ambiente de execução passou a
resolver os domínios necessários).
