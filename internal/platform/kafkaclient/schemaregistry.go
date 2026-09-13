// Package kafkaclient é o kernel de transporte Kafka/Avro da plataforma
// (internal/CLAUDE.md: "admin, producer, serialização Avro genérica").
// Não conhece domínio: recebe schema + registros genéricos e publica.
package kafkaclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// SchemaRegistry é um cliente mínimo da REST API do Confluent Schema
// Registry — o suficiente para registrar um schema e obter o ID que vai no
// wire format. Compatibilidade e evolução são responsabilidade do registry
// (SCHEMA_REGISTRY_SCHEMA_COMPATIBILITY_LEVEL=backward, ADR-0006 §4).
type SchemaRegistry struct {
	BaseURL string
	HTTP    *http.Client
}

// NewSchemaRegistry cria o cliente com timeout curto — registro de schema é
// operação de bootstrap, não de caminho quente.
func NewSchemaRegistry(baseURL string) *SchemaRegistry {
	return &SchemaRegistry{
		BaseURL: baseURL,
		HTTP:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Register registra (ou reencontra, se idêntico) o schema Avro sob o
// subject informado e retorna o ID global atribuído pelo registry.
func (r *SchemaRegistry) Register(ctx context.Context, subject, avroSchema string) (int, error) {
	body, err := json.Marshal(map[string]string{"schema": avroSchema})
	if err != nil {
		return 0, fmt.Errorf("kafkaclient: serializando payload do subject %s: %w", subject, err)
	}

	url := fmt.Sprintf("%s/subjects/%s/versions", r.BaseURL, subject)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("kafkaclient: montando request para %s: %w", url, err)
	}
	req.Header.Set("Content-Type", "application/vnd.schemaregistry.v1+json")

	resp, err := r.HTTP.Do(req)
	if err != nil {
		return 0, fmt.Errorf("kafkaclient: registrando schema em %s: %w", url, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("kafkaclient: lendo resposta de %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("kafkaclient: registry devolveu %d para %s: %s", resp.StatusCode, subject, raw)
	}

	var out struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, fmt.Errorf("kafkaclient: resposta inesperada do registry para %s: %w", subject, err)
	}
	return out.ID, nil
}
