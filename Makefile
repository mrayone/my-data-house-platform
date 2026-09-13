SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help

-include .env
export

GOBIN := $(CURDIR)/bin
PATH  := $(GOBIN):$(PATH)

GOFUMPT_VERSION := v0.7.0

## help: lista os alvos disponíveis
.PHONY: help
help:
	@echo "Alvos disponíveis:"
	@grep -E '^## ' Makefile | sed 's/## /  /'

## tools: instala as ferramentas de dev em ./bin (nunca global)
.PHONY: tools
tools:
	@mkdir -p $(GOBIN)
	@echo ">> instalando gofumpt $(GOFUMPT_VERSION) em ./bin"
	@cd tools && GOBIN=$(GOBIN) GOFLAGS=-mod=mod go install mvdan.cc/gofumpt
	@if [ -x "$(GOBIN)/golangci-lint" ]; then \
		echo ">> golangci-lint já presente em ./bin"; \
	else \
		echo ">> golangci-lint não pôde ser instalado neste ambiente (ver docs/TECH-DEBT.md)."; \
		echo ">> 'make lint' usa 'go vet' como base enquanto o binário não estiver disponível."; \
	fi

## fmt: formata todo o código com gofumpt
.PHONY: fmt
fmt:
	@$(GOBIN)/gofumpt -l -w .

## fmt-check: falha se algum arquivo estiver mal formatado
.PHONY: fmt-check
fmt-check:
	@diff="$$($(GOBIN)/gofumpt -l .)"; \
	if [ -n "$$diff" ]; then \
		echo "Arquivos mal formatados (rode 'make fmt'):"; \
		echo "$$diff"; \
		exit 1; \
	fi

## lint: golangci-lint quando disponível em ./bin, senão go vet como base
.PHONY: lint
lint:
	@if [ -x "$(GOBIN)/golangci-lint" ]; then \
		$(GOBIN)/golangci-lint run; \
	else \
		echo ">> golangci-lint indisponível neste ambiente — rodando 'go vet' como base (ver docs/TECH-DEBT.md)"; \
		go vet ./...; \
	fi

## test: executa os testes com detecção de race
.PHONY: test
test:
	go test ./... -race -count=1

## build: compila os três binários da plataforma em ./bin
.PHONY: build
build:
	@mkdir -p bin
	go build -o bin/dhctl    ./cmd/dhctl
	go build -o bin/producer ./cmd/producer
	go build -o bin/api      ./cmd/api

## verify: fmt-check + lint + test + checks de fronteira de contexto (roda tudo que o CI roda)
.PHONY: verify
verify: fmt-check lint test
	@scripts/checks/context-boundaries.sh

## up: sobe o ambiente local (ClickHouse, Keeper, Kafka, Schema Registry, Connect) e espera ficar healthy
.PHONY: up
up:
	docker compose up -d --build
	@echo ">> aguardando serviços ficarem healthy..."
	@scripts/checks/wait-healthy.sh

## down: derruba o ambiente local (mantém os volumes)
.PHONY: down
down:
	docker compose down

## ps: lista os serviços do ambiente local e o status de saúde
.PHONY: ps
ps:
	docker compose ps

## logs: segue os logs de um serviço (SERVICE=kafka-connect, por exemplo) ou de todos
.PHONY: logs
logs:
	docker compose logs -f --tail=200 $(SERVICE)

## reset-env: derruba o ambiente local e remove os volumes (estado zerado)
.PHONY: reset-env
reset-env:
	docker compose down -v

## db-apply: aplica bootstrap+landing+core+marts no ClickHouse do compose (spike; ver docs/evaluation/spike-mock-flow-clickhouse-kafka.md)
.PHONY: db-apply
db-apply:
	@scripts/db/apply-ddl.sh

## mock-load: gera dados sintéticos (producer mock) e carrega no ClickHouse do compose
.PHONY: mock-load
mock-load:
	@scripts/mock/load.sh

## mock-verify: roda a matriz de verificação do spike (dedup, delete, órfão, ASOF, dictionaries, funil, marts)
.PHONY: mock-verify
mock-verify:
	@scripts/mock/verify.sh

## kafka-topics: cria os tópicos das entidades contratadas + DLQs (partições dos contratos)
.PHONY: kafka-topics
kafka-topics:
	@scripts/kafka/create-topics.sh

## connectors-apply: aplica os ClickHouse Sink connectors (deploy/connect/connectors) no Kafka Connect
.PHONY: connectors-apply
connectors-apply:
	@scripts/kafka/apply-connectors.sh

## kafka-load: publica o dataset mock em Avro nos tópicos Kafka (transporte real: producer -> Kafka -> Connect -> ClickHouse)
.PHONY: kafka-load
kafka-load:
	@mkdir -p bin
	@go build -o bin/producer ./cmd/producer
	@./bin/producer kafka

## kafka-e2e: fluxo completo — DDL + tópicos + connectors + carga Avro + matriz de verificação
.PHONY: kafka-e2e
kafka-e2e: db-apply kafka-topics connectors-apply kafka-load
	@echo ">> aguardando os sinks drenarem os tópicos..."
	@sleep 15
	@scripts/mock/verify.sh
