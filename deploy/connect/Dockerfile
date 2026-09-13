# Kafka Connect com o connector oficial do ClickHouse instalado (ADR-0002).
#
# Requisitos do connector: ClickHouse >= 23.3, Kafka Connect >= 2.7, Java 11+
# (ADR-0002). A imagem base confluentinc/cp-kafka-connect já cobre Connect e
# Java; a versão do ClickHouse é fixada em docker-compose.yml (26.8.3.105).
#
# Plugin: ClickHouse/clickhouse-kafka-connect v1.5.0, copiado do arquivo local
# (clickhouse-kafka-connect-v1.5.0.zip) para evitar dependência de rede no
# build. A imagem base (RHEL 9 UBI minimal) não tem curl/unzip; usa python3
# — presente na imagem — para extrair o zip.
ARG CP_VERSION=8.3.1
FROM confluentinc/cp-kafka-connect:${CP_VERSION}

ARG CLICKHOUSE_CONNECT_VERSION=1.5.0

USER root

COPY clickhouse-kafka-connect-v${CLICKHOUSE_CONNECT_VERSION}.zip /tmp/clickhouse-kafka-connect.zip

RUN mkdir -p /opt/dh-plugins && \
    python3 -c "import zipfile, sys; zipfile.ZipFile(sys.argv[1]).extractall(sys.argv[2])" \
      /tmp/clickhouse-kafka-connect.zip /opt/dh-plugins && \
    rm -f /tmp/clickhouse-kafka-connect.zip && \
    chmod -R a+rX /opt/dh-plugins

# Volta para o usuário não-root original da imagem base do Confluent.
USER 1000
