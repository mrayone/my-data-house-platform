// Package mockgen contem os tipos e helpers compartilhados pelos geradores de
// carga sintetica CDC de cada contexto (internal/contexts/<ctx>/generator).
//
// Cada gerador produz Record (um map por linha), pronto para ser serializado
// como NDJSON e inserido em dh_landing.<ctx>__<entidade>_raw via
// `clickhouse-client --query "INSERT INTO ... FORMAT JSONEachRow"`.
//
// Isto e o "producer" da PoC (ADR-0002) na sua forma de geracao de dados —
// ainda sem o transporte Kafka/Avro real (Fase 03). O modo JSONL existe
// especificamente para testar o fluxo landing->core->marts contra um
// ClickHouse de verdade sem depender de um broker Kafka rodando (ver
// docs/plan/PROGRESS.md, Bloqueios #3 e #4).
package mockgen

import (
	"encoding/json"
	"fmt"
	"time"
)

// Op é o tipo de operação CDC, espelhando o Enum8 de _op nas tabelas de
// landing (ADR-0004 §1).
type Op string

const (
	OpCreate Op = "c"
	OpUpdate Op = "u"
	OpDelete Op = "d"
	OpRead   Op = "r" // snapshot inicial
)

// Record é uma linha pronta para landing: campos de negócio + envelope
// técnico (_topic, _partition, _offset, _kafka_ts, _op, _cdc_seq,
// _schema_id). _ingested_at fica de fora de propósito — a tabela de landing
// tem DEFAULT now64(3) para ela.
type Record map[string]any

// Envelope descreve os metadados técnicos comuns a toda mensagem CDC. Cada
// gerador de entidade preenche Topic e delega a Envelope.Apply para
// carimbar o restante.
type Envelope struct {
	Topic     string
	Partition uint16
	Offset    uint64
	KafkaTS   time.Time
	Op        Op
	CDCSeq    uint64
	SchemaID  uint32
}

// Apply retorna uma cópia de biz com as colunas técnicas adicionadas.
func (e Envelope) Apply(biz Record) Record {
	out := make(Record, len(biz)+8)
	for k, v := range biz {
		out[k] = v
	}
	out["_topic"] = e.Topic
	out["_partition"] = e.Partition
	out["_offset"] = e.Offset
	out["_kafka_ts"] = FormatTimestamp(e.KafkaTS)
	out["_op"] = string(e.Op)
	out["_cdc_seq"] = e.CDCSeq
	out["_schema_id"] = e.SchemaID
	return out
}

// FormatTimestamp formata um time.Time no formato que o ClickHouse
// JSONEachRow aceita para DateTime64, com microssegundo de precisão —
// suficiente tanto para colunas DateTime64(3) quanto DateTime64(6).
func FormatTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05.000000")
}

// FormatDate formata um time.Time como Date/Date32 (sem hora).
func FormatDate(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

// Decimal formata um valor monetário como string decimal fixa — nunca
// float64 no JSON (dq.money.no_float, ADR arquitetural sobre dinheiro).
// scale é o número de casas decimais do tipo Decimal(p,scale) de destino.
func Decimal(value float64, scale int) string {
	return fmt.Sprintf("%.*f", scale, value)
}

// SeqCounter é um contador monotônico simples usado pelos geradores para
// _cdc_seq e _offset. Não é thread-safe — os geradores desta PoC rodam
// sequencialmente.
type SeqCounter struct{ n uint64 }

// Next incrementa e retorna o próximo valor (começa em 1).
func (c *SeqCounter) Next() uint64 {
	c.n++
	return c.n
}

// WriteNDJSON serializa records como NDJSON (uma linha JSON por registro),
// no formato que `FORMAT JSONEachRow` espera.
func WriteNDJSON(records []Record) ([]byte, error) {
	var buf []byte
	for _, r := range records {
		line, err := json.Marshal(r)
		if err != nil {
			return nil, fmt.Errorf("mockgen: falha ao serializar registro: %w", err)
		}
		buf = append(buf, line...)
		buf = append(buf, '\n')
	}
	return buf, nil
}
