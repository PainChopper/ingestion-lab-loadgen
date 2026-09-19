package main

import "context"

func startThrottler(
	ctx context.Context,
	readerBatches <-chan []Transaction,
	readerChannel *readerChannelTelemetry,
) (<-chan []Transaction, <-chan struct{}) {
	senderBatches := make(chan []Transaction)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(senderBatches)
		for {
			var batch []Transaction
			select {
			case <-ctx.Done():
				return
			case next, ok := <-readerBatches:
				if !ok {
					return
				}
				batch = next
				readerChannel.recordReceive(len(batch))
			}

			select {
			case <-ctx.Done():
				return
			case senderBatches <- batch:
			}
		}
	}()
	return senderBatches, done
}
