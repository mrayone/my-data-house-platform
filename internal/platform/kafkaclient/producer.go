package kafkaclient

import (
	"context"
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"
)

// Producer publica mensagens já serializadas. Produção é síncrona e
// sequencial de propósito: a ordem de emissão dos geradores é parte do
// dataset (fora-de-ordem deliberado de _cdc_seq dentro da mesma chave), e
// só a produção 1-a-1 garante que ela chega ao broker como foi emitida.
type Producer struct {
	client *kgo.Client
}

// NewProducer conecta nos brokers informados.
func NewProducer(brokers []string) (*Producer, error) {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		// Idempotência fica ligada por default no franz-go; acks=all.
		kgo.ProducerBatchCompression(kgo.SnappyCompression()),
	)
	if err != nil {
		return nil, fmt.Errorf("kafkaclient: conectando em %v: %w", brokers, err)
	}
	return &Producer{client: client}, nil
}

// ProduceSync publica uma mensagem e espera o ack do broker.
func (p *Producer) ProduceSync(ctx context.Context, topic string, key, value []byte) error {
	rec := &kgo.Record{Topic: topic, Key: key, Value: value}
	if err := p.client.ProduceSync(ctx, rec).FirstErr(); err != nil {
		return fmt.Errorf("kafkaclient: produzindo em %s: %w", topic, err)
	}
	return nil
}

// Close descarrega buffers pendentes e encerra a conexão.
func (p *Producer) Close() {
	p.client.Close()
}
