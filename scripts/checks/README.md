# Checks estáticos

Scripts que substituem revisão humana para regras que não podem quebrar em
silêncio. Todo check novo entra nesta tabela.

| Script | O que impede | ADR de origem | Introduzido em |
|---|---|---|---|
| `context-boundaries.sh` | contexto importando outro contexto; `platform` importando `contexts` | [ADR-0009](../../docs/adr/0009-layout-de-pastas-context-first.md) | Fase 00 |

## Convenção

Todo script neste diretório:

- começa com `#!/usr/bin/env bash` e `set -euo pipefail`;
- é executável (`chmod +x`);
- ao falhar, imprime **qual regra** foi violada e **como corrigir** — nunca só
  "falhou";
- é chamado por `make verify`, nunca duplicado à mão no CI
  (`.github/workflows/ci.yml`).
