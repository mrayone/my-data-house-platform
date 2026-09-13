package kafkaclient

import (
	"encoding/binary"
	"fmt"
	"math/big"
	"time"

	"github.com/hamba/avro/v2"
)

// Codec serializa registros genéricos (map[string]any) no wire format do
// Confluent: 1 byte mágico 0x00 + 4 bytes big-endian de schema ID + payload
// Avro binário. É o formato que o AvroConverter do Kafka Connect espera.
type Codec struct {
	Schema   avro.Schema
	SchemaID int
}

// NewCodec faz o parse do .avsc e amarra o schema ao ID devolvido pelo
// registry.
func NewCodec(avroSchema string, schemaID int) (*Codec, error) {
	s, err := avro.Parse(avroSchema)
	if err != nil {
		return nil, fmt.Errorf("kafkaclient: parse do schema Avro: %w", err)
	}
	return &Codec{Schema: s, SchemaID: schemaID}, nil
}

// Encode serializa um registro no wire format Confluent. O registro é
// coagido campo a campo aos tipos que o encoder Avro espera (os geradores
// emitem uint16/uint64/int32 etc., que não mapeiam 1:1 nos tipos Avro).
func (c *Codec) Encode(record map[string]any) ([]byte, error) {
	coerced, err := coerceRecord(c.Schema, record)
	if err != nil {
		return nil, err
	}
	payload, err := avro.Marshal(c.Schema, coerced)
	if err != nil {
		return nil, fmt.Errorf("kafkaclient: serializando registro Avro: %w", err)
	}

	out := make([]byte, 0, 5+len(payload))
	out = append(out, 0x00)
	out = binary.BigEndian.AppendUint32(out, uint32(c.SchemaID))
	out = append(out, payload...)
	return out, nil
}

// coerceRecord adapta os valores Go do registro aos tipos do schema Avro:
// inteiros de qualquer largura viram int/int64 conforme o campo, e campos
// union nuláveis aceitam nil ou o valor direto.
func coerceRecord(schema avro.Schema, record map[string]any) (map[string]any, error) {
	rec, ok := schema.(*avro.RecordSchema)
	if !ok {
		return nil, fmt.Errorf("kafkaclient: schema raiz não é record (é %s)", schema.Type())
	}

	out := make(map[string]any, len(rec.Fields()))
	for _, f := range rec.Fields() {
		v, present := record[f.Name()]
		if !present {
			return nil, fmt.Errorf("kafkaclient: campo %q ausente no registro", f.Name())
		}
		cv, err := coerceValue(f.Type(), v)
		if err != nil {
			return nil, fmt.Errorf("kafkaclient: campo %q: %w", f.Name(), err)
		}
		out[f.Name()] = cv
	}
	return out, nil
}

func coerceValue(schema avro.Schema, v any) (any, error) {
	switch s := schema.(type) {
	case *avro.UnionSchema:
		if v == nil {
			return nil, nil
		}
		// União nulável (["null", T]): coage para o ramo não-nulo.
		for _, branch := range s.Types() {
			if branch.Type() != avro.Null {
				return coerceValue(branch, v)
			}
		}
		return nil, fmt.Errorf("união sem ramo não-nulo para valor %v", v)
	case *avro.PrimitiveSchema:
		// bytes + logicalType decimal: os geradores emitem dinheiro como
		// string decimal fixa (mockgen.Decimal); o encoder Avro espera
		// *big.Rat para o logical type decimal.
		if lt := s.Logical(); lt != nil {
			switch lt.Type() {
			case avro.Decimal:
				str, ok := v.(string)
				if !ok {
					return nil, fmt.Errorf("esperado string decimal, veio %T (%v)", v, v)
				}
				rat, ok := new(big.Rat).SetString(str)
				if !ok {
					return nil, fmt.Errorf("string decimal invalida: %q", str)
				}
				return rat, nil
			case avro.TimestampMillis, avro.TimestampMicros:
				return parseTime(v, "2006-01-02 15:04:05.000000")
			case avro.Date:
				return parseTime(v, "2006-01-02")
			}
		}
		switch s.Type() {
		case avro.Int:
			n, err := toInt64(v)
			if err != nil {
				return nil, err
			}
			return int(n), nil
		case avro.Long:
			return toInt64(v)
		case avro.String:
			str, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("esperado string, veio %T (%v)", v, v)
			}
			return str, nil
		default:
			return v, nil
		}
	default:
		return v, nil
	}
}

// parseTime converte os formatos de string do mockgen (FormatTimestamp /
// FormatDate) para time.Time UTC — o tipo que o encoder Avro espera nos
// logical types timestamp-millis e date.
func parseTime(v any, layout string) (time.Time, error) {
	str, ok := v.(string)
	if !ok {
		return time.Time{}, fmt.Errorf("esperado string de tempo, veio %T (%v)", v, v)
	}
	t, err := time.ParseInLocation(layout, str, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("tempo invalido %q: %w", str, err)
	}
	return t, nil
}

func toInt64(v any) (int64, error) {
	switch n := v.(type) {
	case int:
		return int64(n), nil
	case int8:
		return int64(n), nil
	case int16:
		return int64(n), nil
	case int32:
		return int64(n), nil
	case int64:
		return n, nil
	case uint8:
		return int64(n), nil
	case uint16:
		return int64(n), nil
	case uint32:
		return int64(n), nil
	case uint64:
		return int64(n), nil
	default:
		return 0, fmt.Errorf("esperado inteiro, veio %T (%v)", v, v)
	}
}
