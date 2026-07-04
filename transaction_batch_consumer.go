package main

func consumeBatches(batches <-chan TransactionBatch) {
	for batch := range batches {
		for i := range batch.Transactions {
			consumeTransaction(&batch.Transactions[i])
		}
	}
}
