#!/usr/bin/env bash
set -euo pipefail

# Usado por `make up`. docker compose respeita `depends_on: condition:
# service_healthy` na ORDEM de subida, mas o comando `up -d` pode retornar
# antes do último serviço da cadeia reportar healthy. Este script fecha essa
# lacuna: espera todos os serviços com healthcheck definido ficarem "healthy",
# e falha com mensagem clara (nome do serviço + estado) em timeout — nunca
# devolve verde com serviço ainda subindo (deploy/CLAUDE.md #8).

TIMEOUT_SECONDS="${WAIT_HEALTHY_TIMEOUT:-180}"
INTERVAL_SECONDS=3
elapsed=0

services_with_healthcheck() {
	docker compose config --format json 2>/dev/null \
		| python3 -c "
import json, sys
cfg = json.load(sys.stdin)
for name, svc in cfg.get('services', {}).items():
    if 'healthcheck' in svc:
        print(name)
"
}

SERVICES="$(services_with_healthcheck)"
if [ -z "$SERVICES" ]; then
	echo "wait-healthy: nenhum serviço com healthcheck encontrado em docker-compose.yml"
	exit 1
fi

while true; do
	all_healthy=1
	status_line=""
	for svc in $SERVICES; do
		cid="$(docker compose ps -q "$svc" 2>/dev/null || true)"
		if [ -z "$cid" ]; then
			health="sem-container"
		else
			health="$(docker inspect --format='{{if .State.Health}}{{.State.Health.Status}}{{else}}sem-healthcheck{{end}}' "$cid" 2>/dev/null || echo "erro-inspect")"
		fi
		status_line="$status_line $svc=$health"
		[ "$health" = "healthy" ] || all_healthy=0
	done

	if [ "$all_healthy" -eq 1 ]; then
		echo "wait-healthy: todos os serviços healthy —$status_line"
		exit 0
	fi

	if [ "$elapsed" -ge "$TIMEOUT_SECONDS" ]; then
		echo "wait-healthy: TIMEOUT após ${TIMEOUT_SECONDS}s —$status_line"
		echo "wait-healthy: rode 'make logs SERVICE=<nome>' para diagnosticar o serviço que não ficou healthy"
		exit 1
	fi

	sleep "$INTERVAL_SECONDS"
	elapsed=$((elapsed + INTERVAL_SECONDS))
done
