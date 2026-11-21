package pipeline

import "sync/atomic"

type readerTelemetry struct {
	rowsRead atomic.Int64
}

func (r *readerTelemetry) recordRead(n int) {
	if n <= 0 {
		return
	}
	r.rowsRead.Add(int64(n))
}

func (r *readerTelemetry) snapshot() int64 {
	return r.rowsRead.Load()
}
