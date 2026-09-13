# Kafka Connect com o connector oficial do ClickHouse instalado (ADR-0002).
#
# Requisitos do connector: ClickHouse >= 23.3, Kafka Connect >= 2.7, Java 11+
# (ADR-0002). A imagem base confluentinc/cp-kafka-connect já cobre Connect e
# Java; a versão do ClickHouse é fixada em docker-compose.yml (26.8.3.105).
#
# Plugin: ClickHouse/clickhouse-kafka-connect, baixado do release oficial no
# GitHub (não publicado no Confluent Hub) — ver
# https://github.com/ClickHouse/clickhouse-kafka-connect/releases
#
# NOTA (desta sessão de execução): este Dockerfile não pôde ser construído nem
# testado aqui — o ambiente sandboxed não tem Docker instalado (ver
# docs/plan/PROGRESS.md, Bloqueios #3). Validar com `make up` numa máquina com
# Docker antes de considerar a Fase 01 concluída.
ARG CP_VERSION=8.5.0
FROM confluentinc/cp-kafka-connect:${CP_VERSION}

ARG CLICKHOUSE_CONNECT_VERSION=1.5.0
ARG CLICKHOUSE_CONNECT_SHA256

USER root

# curl e unzip podem já vir na imagem base (varia por versão do CP); instala
# se faltar, sem falhar caso o gerenciador de pacotes não exista/tenha mudado.
RUN (command -v curl >/dev/null && command -v unzip >/dev/null) || \
    (microdnf install -y curl unzip 2>/dev/null) || \
    (yum install -y curl unzip 2>/dev/null) || \
    (apt-get update && apt-get install -y curl unzip 2>/dev/null) || true

RUN mkdir -p /opt/dh-plugins && \
    curl -fsSL -o /tmp/clickhouse-kafka-connect.zip \
      "https://github.com/ClickHouse/clickhouse-kafka-connect/releases/download/v${CLICKHOUSE_CONNECT_VERSION}/clickhouse-kafka-connect-v${CLICKHOUSE_CONNECT_VERSION}.zip" && \
    if [ -n "${CLICKHOUSE_CONNECT_SHA256}" ]; then \
      echo "${CLICKHOUSE_CONNECT_SHA256}  /tmp/clickhouse-kafka-connect.zip" | sha256sum -c - ; \
    fi && \
    unzip -q /tmp/clickhouse-kafka-connect.zip -d /opt/dh-plugins && \
    rm -f /tmp/clickhouse-kafka-connect.zip && \
    chmod -R a+rX /opt/dh-plugins

# Volta para o usuário não-root original da imagem base do Confluent, seja
# qual for o nome/UID nesta versão — evita chutar "appuser" e quebrar em
# builds futuros se o nome mudar.
USER 1000
