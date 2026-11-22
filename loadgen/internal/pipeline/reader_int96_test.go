package pipeline

import (
	"strings"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
)

func TestInt96EventTimeProjectionRejectsIncompatibleField(t *testing.T) {
	t.Parallel()

	schema := parquet.SchemaOf(struct {
		EventTime time.Duration `parquet:"event_time,optional"`
	}{})
	source := parquet.Optional(parquet.Leaf(parquet.Int96Type))

	if _, err := int96EventTimeProjection(schema, source); err == nil ||
		!strings.Contains(err.Error(), "event_time must have Go type time.Time, got time.Duration") {
		t.Fatalf("int96 projection error = %v, want a field type error", err)
	}
}
