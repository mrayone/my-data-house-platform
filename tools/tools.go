//go:build tools

// Package tools fixa (via import em branco) as versões das ferramentas de
// desenvolvimento instaladas por `make tools`. O build tag `tools` garante que
// este arquivo nunca entra em um build normal — só existe para `go.mod`
// registrar a versão exata via `go install <import>@<versão>` no Makefile.
//
// golangci-lint não pôde ser fixado aqui: sua árvore de dependências usa vários
// domínios de vanity import (golang.org/x/*, honnef.co/go/tools, go-simpler.org,
// go.uber.org) que a rede deste ambiente de execução não resolve. Ver
// docs/TECH-DEBT.md e o bloqueio registrado em docs/plan/PROGRESS.md (Fase 00).
// gofumpt foi viável porque suas dependências (golang.org/x/sync,
// golang.org/x/tools, golang.org/x/mod, github.com/google/go-cmp) puderam ser
// resolvidas via `replace` direto para os espelhos em github.com/golang/*.
package tools

import (
	_ "mvdan.cc/gofumpt"
)
