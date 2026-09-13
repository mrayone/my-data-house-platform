#!/usr/bin/env bash
set -euo pipefail

# Verifica as regras 4 e 5 do ADR-0009:
#   1. um pacote sob internal/contexts/<a>/ não pode importar
#      internal/contexts/<b>/ com a != b.
#   2. um pacote sob internal/platform/ não pode importar internal/contexts/.
#
# Usa `go list -deps` (não regex sobre imports) para não dar falso positivo em
# comentário ou string. go list -deps inclui o próprio pacote no fecho de
# dependências — isso é esperado e não é uma violação.

cd "$(git rev-parse --show-toplevel)"

MODULE="github.com/mrayone/my-data-house-platform"
FAIL=0

fail() {
	echo "ERRO: $1"
	echo "  Como corrigir: $2"
	FAIL=1
}

# --- Regra 1: contexto não importa outro contexto -----------------------
for ctx_dir in internal/contexts/*/; do
	ctx="$(basename "$ctx_dir")"
	pkgs="$(go list "./${ctx_dir}..." 2>/dev/null || true)"
	[ -z "$pkgs" ] && continue

	while IFS= read -r pkg; do
		[ -z "$pkg" ] && continue
		deps="$(go list -deps "$pkg" 2>/dev/null || true)"
		while IFS= read -r dep; do
			[ -z "$dep" ] && continue
			case "$dep" in
				"$MODULE/internal/contexts/"*)
					rest="${dep#"$MODULE/internal/contexts/"}"
					other_ctx="${rest%%/*}"
					if [ "$other_ctx" != "$ctx" ]; then
						fail \
							"pacote '$pkg' (contexto '$ctx') importa '$dep' (contexto '$other_ctx')" \
							"mova a lógica compartilhada para internal/contexts/analytics ou internal/platform"
					fi
					;;
			esac
		done <<< "$deps"
	done <<< "$pkgs"
done

# --- Regra 2: platform não importa contexts ------------------------------
pkgs="$(go list ./internal/platform/... 2>/dev/null || true)"
while IFS= read -r pkg; do
	[ -z "$pkg" ] && continue
	deps="$(go list -deps "$pkg" 2>/dev/null || true)"
	while IFS= read -r dep; do
		[ -z "$dep" ] && continue
		case "$dep" in
			"$MODULE/internal/contexts/"*)
				fail \
					"pacote '$pkg' (platform) importa '$dep' (contexts)" \
					"mova o código que depende de contexto para internal/contexts/<ctx>; platform é o kernel compartilhado e nunca conhece contexto"
				;;
		esac
	done <<< "$deps"
done <<< "$pkgs"

if [ "$FAIL" -ne 0 ]; then
	echo ""
	echo "context-boundaries.sh: FALHOU — ver ADR-0009."
	exit 1
fi

echo "context-boundaries.sh: OK — nenhuma violação de fronteira de contexto."
