package config

import (
	"os"
	"strings"
	"testing"
)

func clearEnv(t *testing.T) {
	t.Helper()
	vars := []string{"CLICKHOUSE_DSN", "KAFKA_BROKERS", "SCHEMA_REGISTRY_URL", "CONNECT_URL", "LOG_LEVEL"}
	for _, v := range vars {
		old, had := os.LookupEnv(v)
		os.Unsetenv(v)
		t.Cleanup(func() {
			if had {
				os.Setenv(v, old)
			}
		})
	}
}

func TestLoadFailsWithClearMessageWhenEnvEmpty(t *testing.T) {
	clearEnv(t)

	_, err := Load()
	if err == nil {
		t.Fatal("esperava erro com ambiente vazio, obteve nil")
	}
	if !strings.Contains(err.Error(), "CLICKHOUSE_DSN") {
		t.Errorf("erro deveria nomear a variável ausente CLICKHOUSE_DSN, obteve: %v", err)
	}
}

func TestLoadSucceedsWithAllRequiredVars(t *testing.T) {
	clearEnv(t)
	os.Setenv("CLICKHOUSE_DSN", "clickhouse://localhost:9000/default")
	os.Setenv("KAFKA_BROKERS", "localhost:9092")
	os.Setenv("SCHEMA_REGISTRY_URL", "http://localhost:8081")
	os.Setenv("CONNECT_URL", "http://localhost:8083")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() retornou erro inesperado: %v", err)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel default deveria ser \"info\", obteve %q", cfg.LogLevel)
	}
	if cfg.ClickHouseDSN == "" || cfg.KafkaBrokers == "" || cfg.SchemaRegistryURL == "" || cfg.ConnectURL == "" {
		t.Errorf("todos os campos deveriam estar preenchidos: %+v", cfg)
	}
}

func TestLoadRespectsExplicitLogLevel(t *testing.T) {
	clearEnv(t)
	os.Setenv("CLICKHOUSE_DSN", "x")
	os.Setenv("KAFKA_BROKERS", "x")
	os.Setenv("SCHEMA_REGISTRY_URL", "x")
	os.Setenv("CONNECT_URL", "x")
	os.Setenv("LOG_LEVEL", "debug")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() retornou erro inesperado: %v", err)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel deveria respeitar env, obteve %q", cfg.LogLevel)
	}
}
