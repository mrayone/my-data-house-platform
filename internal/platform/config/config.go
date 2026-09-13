// Package config carrega a configuração da plataforma a partir de variáveis
// de ambiente.
//
// Nenhum valor sensível (senha, token, DSN de produção) deve ser
// hard-coded neste pacote ou em qualquer outro — sempre vem do ambiente. Ver
// `.env.example` na raiz do repositório para a lista completa de variáveis
// lidas por Load.
package config

import (
	"fmt"
	"os"
)

// Config agrega os endereços de infraestrutura que os binários da
// plataforma (dhctl, producer, api) precisam para se conectar aos serviços
// externos.
type Config struct {
	// ClickHouseDSN é o DSN de conexão com o ClickHouse (ex.:
	// "clickhouse://localhost:9000/default").
	ClickHouseDSN string

	// KafkaBrokers é a lista de brokers Kafka separada por vírgula (ex.:
	// "localhost:9092").
	KafkaBrokers string

	// SchemaRegistryURL é o endereço HTTP do Confluent Schema Registry.
	SchemaRegistryURL string

	// ConnectURL é o endereço HTTP do Kafka Connect (REST API).
	ConnectURL string

	// LogLevel é repassado para internal/platform/logging. Não é
	// obrigatória — default "info".
	LogLevel string
}

// requiredEnvVars lista, em ordem, as variáveis de ambiente obrigatórias e o
// campo de Config que cada uma preenche.
var requiredEnvVars = []struct {
	name string
	set  func(*Config, string)
}{
	{"CLICKHOUSE_DSN", func(c *Config, v string) { c.ClickHouseDSN = v }},
	{"KAFKA_BROKERS", func(c *Config, v string) { c.KafkaBrokers = v }},
	{"SCHEMA_REGISTRY_URL", func(c *Config, v string) { c.SchemaRegistryURL = v }},
	{"CONNECT_URL", func(c *Config, v string) { c.ConnectURL = v }},
}

// Load lê a configuração do ambiente do processo.
//
// Falha com um erro nomeando a primeira variável obrigatória ausente. LOG_LEVEL
// é opcional e usa "info" como default quando não definida.
func Load() (Config, error) {
	var cfg Config

	for _, v := range requiredEnvVars {
		val, ok := os.LookupEnv(v.name)
		if !ok || val == "" {
			return Config{}, fmt.Errorf("config: variável de ambiente obrigatória ausente: %s", v.name)
		}
		v.set(&cfg, val)
	}

	cfg.LogLevel = os.Getenv("LOG_LEVEL")
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}

	return cfg, nil
}
