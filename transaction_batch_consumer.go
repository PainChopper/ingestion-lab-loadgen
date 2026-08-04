package main

import "sync/atomic"

const progressEvery int64 = 500

func consumeBatches(batches <-chan []Transaction, consumedSinceTick *atomic.Int64) {
	var pending int64
	for batch := range batches {
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
