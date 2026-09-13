# ADR-0009 — Organizar o repositório por contexto, com SQL ordenado por camada

- **Status:** Accepted
- **Data:** 2026-09-13
- **Decisores:** Engenharia de Software
- **ADRs relacionados:** 0001, 0003, 0007

## Contexto

O requisito pede *"estrutura de pastas focadas em contexto, onde possa ser
extensível"*. Com ~30 entidades e crescimento contínuo, o layout decide se o
repositório fica navegável ou vira um depósito.

Duas forças em tensão:

- **Coesão por contexto:** tudo de `sales` junto, para que quem mexe em vendas
  não navegue o repositório inteiro, e para que um contexto novo seja uma pasta.
- **Ordem global de execução do SQL:** MV não pode ser criada antes da tabela
  destino, e marts cruzam contextos — isso é ordem **por camada**, que atravessa
  os contextos.

Layout por tipo (`/sql`, `/models`, `/consumers`) satisfaz a segunda e mata a
primeira. Layout por contexto puro satisfaz a primeira e quebra a segunda.

## Decisão

**Contexto é a pasta de primeiro nível; camada é a pasta de segundo nível dentro
do contexto. A ordenação global é reconstruída pelo runner a partir do número da
camada** (ADR-0007).

```
internal/contexts/<contexto>/
├── CLAUDE.md          <- o que é este contexto, entidades, relações, donos
├── generator/         <- geração de carga CDC sintética deste contexto (Go)
├── reports/           <- queries dos cenários que PERTENCEM a este contexto (Go)
└── sql/
    ├── 10-landing/    <- GERADO por dhctl (ADR-0003)
    ├── 20-core/       <- tabela core + MV incremental + v_*_current
    ├── 30-marts/      <- dictionaries e refreshable MVs do contexto
    └── 40-reports/    <- views de leitura do contexto
```

Regras:

1. **Adicionar contexto = criar pasta.** Nada a registrar em arquivo central,
   nada a renumerar. O runner descobre por glob.
2. **Numeração de camada é global e fixa** (`10/20/30/40`). Camada nova exige ADR.
3. **`analytics` é um contexto de verdade**, sem tópico próprio, onde vivem os
   marts que cruzam contextos (`order_360`, `revenue_by_bu_day`). O runner o
   ordena por último dentro de cada camada. Isso evita a pergunta "de qual
   contexto é o `order_360`?" — é do `analytics`, que depende dos outros.
4. **Nada cross-context dentro de um contexto.** SQL em
   `internal/contexts/sales/` só referencia `dh_landing.sales__*`,
   `dh_core.sales__*` e dictionaries. Cruzou contexto? Vai para `analytics`.
   Verificável por check estático.
5. **`internal/platform/` é o kernel compartilhado** e **não conhece contexto
   nenhum**. Dependência sempre `contexts/* → platform`, nunca o contrário e
   nunca `contexts/a → contexts/b`.
6. **Contratos e schemas espelham os mesmos contextos**
   (`contracts/domains/<ctx>/`, `schemas/avro/<ctx>/`), porque contexto é o eixo
   do sistema em todas as dimensões.
7. **`cmd/` é fino.** Só parsing de flag e wiring; lógica em `internal/`.
8. **`db/` guarda apenas o que não pertence a contexto algum:**
   `db/shared/00-bootstrap/` (databases, `dh_meta`, roles).

## Alternativas consideradas

### A) Layout por tipo técnico (`/sql/landing`, `/sql/core`, `/go/consumers`)
- **Prós:** ordem de execução óbvia; familiar a quem vem de dbt.
- **Contras:** o contexto se espalha por 6 pastas; adicionar entidade toca em 6
  lugares distantes; ninguém consegue ler "tudo de vendas".
- **Por que não:** contraria o requisito explícito de foco em contexto.

### B) Contexto puro com numeração global de migration (`db/migrations/0042__*.sql`)
- **Prós:** ordem trivial.
- **Contras:** duas branches que adicionam entidade colidem no número; ordenação
  vira coordenação humana.
- **Por que não:** conflito de merge garantido com vários agentes trabalhando em
  paralelo — exatamente o cenário deste projeto.

### C) Um módulo Go por contexto (multi-módulo)
- **Prós:** fronteira forte de dependência.
- **Contras:** overhead de versionamento entre módulos numa PoC.
- **Por que não:** a fronteira é obtida pelo check de import (regra 5) a custo
  muito menor. Fica como evolução se o repositório crescer para times separados.

## Consequências

### Positivas
- Contexto novo é uma pasta e zero conflito de merge.
- Ordem de execução é derivada, não mantida à mão.
- Fronteira de dependência explícita e verificável.
- Agentes paralelos em contextos diferentes não colidem.

### Negativas / custo aceito
- SQL de um mesmo assunto pode estar em dois contextos (core em `sales`, mart em
  `analytics`). Mitigado: o doc do cenário aponta todos os arquivos envolvidos.
- Precisamos dos checks de fronteira, senão a regra 4/5 apodrece.
- `internal/contexts/*/sql/` guarda SQL dentro de uma árvore Go, o que estranha
  à primeira vista. É deliberado: coesão do contexto vence a convenção de
  linguagem, e o `go:embed` dessas pastas facilita o binário único do `dhctl`.

## Impacto no repositório

Todo o layout. `scripts/checks/context-boundaries.sh`,
`scripts/checks/no-cross-context-sql.sh`, `CLAUDE.md` por contexto.

## Como validar

```bash
scripts/checks/context-boundaries.sh      # contexts não importam contexts
scripts/checks/no-cross-context-sql.sh    # SQL de contexto não cruza contexto
dhctl migrate --status                     # ordem canônica por camada
test -f internal/contexts/sales/CLAUDE.md  # todo contexto documentado
```
