package main

import (
	"context"
	"sync/atomic"
)

const progressEvery int64 = 500

func consumeBatches(
	ctx context.Context,
	batches <-chan []Transaction,
	consumedSinceTick *atomic.Int64,
	readerChannelTelemetry *readerChannelTelemetry,
) {
	var pending int64
	for {
		if ctx.Err() != nil {
			return
		}
		var batch []Transaction
		select {
		case <-ctx.Done():
			return
		case next, ok := <-batches:
			if !ok {
				return
			}
			batch = next
			readerChannelTelemetry.recordReceive(len(batch))
		}
		for i := range batch {
			consumeTransaction(&batch[i])
			pending++
			if pending == progressEvery {
				consumedSinceTick.Add(pending)
				pending = 0
			}
		}
		if pending > 0 {
			consumedSinceTick.Add(pending)
			pending = 0
		}
	}
}
