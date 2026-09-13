package mockrun

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrayone/my-data-house-platform/internal/platform/kafkaclient"
)

// PublishOptions parametriza a publicação dos datasets mock nos tópicos
// Kafka reais — o transporte da Fase 03, exercitado aqui com o dataset do
// spike.
type PublishOptions struct {
	Brokers      []string
	RegistryURL  string
	SchemasDir   string          // raiz de schemas/avro (contém <ctx>/<entidade>.avsc)
	OnlyDatasets map[string]bool // filtro opcional "<ctx>__<entidade>"; vazio = todos
}

// PublishKafka registra o schema Avro de cada dataset no Schema Registry e
// publica os registros no tópico da entidade (campo _topic do envelope), na
// ordem de emissão do gerador — preservando o fora-de-ordem deliberado de
// _cdc_seq dentro da mesma chave.
func PublishKafka(ctx context.Context, log *slog.Logger, datasets []Dataset, opts PublishOptions) error {
	registry := kafkaclient.NewSchemaRegistry(opts.RegistryURL)

	producer, err := kafkaclient.NewProducer(opts.Brokers)
	if err != nil {
		return err
	}
	defer producer.Close()

	for _, d := range datasets {
		name := fmt.Sprintf("%s__%s", d.Context, d.Entity)
		if len(opts.OnlyDatasets) > 0 && !opts.OnlyDatasets[name] {
			continue
		}
		if len(d.Records) == 0 {
			continue
		}

		topic, ok := d.Records[0]["_topic"].(string)
		if !ok || topic == "" {
			return fmt.Errorf("mockrun: dataset %s sem _topic no envelope", name)
		}

		schemaPath := filepath.Join(opts.SchemasDir, d.Context, d.Entity+".avsc")
		schemaJSON, err := os.ReadFile(schemaPath)
		if err != nil {
			return fmt.Errorf("mockrun: lendo schema %s: %w", schemaPath, err)
		}

		subject := topic + "-value"
		schemaID, err := registry.Register(ctx, subject, string(schemaJSON))
		if err != nil {
			return err
		}

		codec, err := kafkaclient.NewCodec(string(schemaJSON), schemaID)
		if err != nil {
			return fmt.Errorf("mockrun: schema %s: %w", schemaPath, err)
		}

		for i, rec := range d.Records {
			value, err := codec.Encode(rec)
			if err != nil {
				return fmt.Errorf("mockrun: %s registro %d: %w", name, i, err)
			}
			key := messageKey(d.KeyFields, rec)
			if err := producer.ProduceSync(ctx, topic, key, value); err != nil {
				return fmt.Errorf("mockrun: %s registro %d: %w", name, i, err)
			}
		}

		log.Info("producer kafka", "context", d.Context, "entity", d.Entity,
			"topic", topic, "subject", subject, "schema_id", schemaID,
			"records", len(d.Records))
	}
	return nil
}

// messageKey monta a key Kafka concatenando os campos de chave de negócio.
// Mensagens da mesma entidade-instância caem na mesma partição, preservando
// a ordem relativa entre versões CDC da mesma chave.
func messageKey(fields []string, rec map[string]any) []byte {
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		parts = append(parts, fmt.Sprintf("%v", rec[f]))
	}
	return []byte(strings.Join(parts, "|"))
}
