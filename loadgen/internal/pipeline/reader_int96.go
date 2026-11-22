package pipeline

import (
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/parquet-go/parquet-go"
)

const eventTimeColumn = "event_time"

func int96EventTimeProjection(schema *parquet.Schema, source parquet.Node) (*parquet.Schema, error) {
	fields := slices.Clone(schema.Fields())
	for i, field := range fields {
		if field.Name() != eventTimeColumn {
			continue
		}

		// Inspect the struct field, since a Parquet optional node can report a pointer type.
		got := field.Value(reflect.New(schema.GoType()).Elem()).Type()
		if got != reflect.TypeFor[time.Time]() {
			return nil, fmt.Errorf("int96 %s must have Go type time.Time, got %v", eventTimeColumn, got)
		}

		// parquet-go v0.32.0 falls back to INT64 conversion, dropping the Julian day.
		// Keep INT96 through projection and decode both parts here. See https://github.com/segmentio/parquet-go/pull/506.
		fields[i] = int96TimestampField{Field: field, source: source}
		return parquet.NewSchema(schema.Name(), &parquetProjection{Node: schema, fields: fields}), nil
	}
	return nil, fmt.Errorf("int96 %s is missing from the destination schema", eventTimeColumn)
}

type parquetProjection struct {
	parquet.Node
	fields []parquet.Field
}

func (p *parquetProjection) Fields() []parquet.Field { return p.fields }

type int96TimestampField struct {
	parquet.Field
	source parquet.Node
}

func (f int96TimestampField) Type() parquet.Type { return int96Timestamp{Type: f.source.Type()} }

// Keep a missing timestamp null through projection so it reconstructs as zero time.
func (f int96TimestampField) Optional() bool { return f.source.Optional() }
func (f int96TimestampField) Required() bool { return !f.Optional() }

type int96Timestamp struct{ parquet.Type }

func (int96Timestamp) AssignValue(dst reflect.Value, src parquet.Value) error {
	if src.IsNull() {
		dst.SetZero()
		return nil
	}
	if size := len(src.ByteArray()); size != 12 {
		return fmt.Errorf("invalid int96 timestamp: %d bytes", size)
	}
	value := src.Int96()
	const unixEpochJulianDay = 2440588
	const day = 24 * time.Hour
	// The low 64 bits hold nanoseconds since midnight. The high word holds the Julian day.
	nanosOfDay := value.Int64()
	if nanosOfDay < 0 || nanosOfDay >= int64(day) {
		return fmt.Errorf("invalid int96 timestamp: nanoseconds of day %d", nanosOfDay)
	}
	seconds := (int64(value[2]) - unixEpochJulianDay) * int64(day/time.Second)
	dst.Set(reflect.ValueOf(time.Unix(seconds, nanosOfDay).UTC()))
	return nil
}
