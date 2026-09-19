package main

import (
	"encoding/json"
	"log"
	"net/http"
)

type statusSnapshot struct {
	RunState                          runState       `json:"runState"`
	TotalTransactions                 int64          `json:"totalTransactions"`
	ReaderWorkers                     int            `json:"readerWorkers"`
	ReaderReadBatchSize               int            `json:"readerReadBatchSize"`
	SenderWorkers                     int            `json:"senderWorkers"`
	ElapsedMs                         int64          `json:"elapsedMs"`
	StartError                        *string        `json:"startError"`
	ReaderReadTPS                     float64        `json:"readerReadTps"`
	ReaderRowsRead                    int64          `json:"readerRowsRead"`
	ReaderSource                      *string        `json:"readerSource"`
	Queue1Capacity                    int            `json:"queue1Capacity"`
	Queue1DepthBatches                int            `json:"queue1DepthBatches"`
	Queue1QueuedTransactions          int            `json:"queue1QueuedTransactions"`
	Queue1BlockedSenders              int            `json:"queue1BlockedSenders"`
	Queue1OldestBlockedSenderMs       int64          `json:"queue1OldestBlockedSenderMs"`
	Queue1BlockedMs                   int64          `json:"queue1BlockedMs"`
	Queue1EnqueuedBatchesTotal        int64          `json:"queue1EnqueuedBatchesTotal"`
	Queue1EnqueuedTransactionsTotal   int64          `json:"queue1EnqueuedTransactionsTotal"`
	Queue1DequeuedBatchesTotal        int64          `json:"queue1DequeuedBatchesTotal"`
	Queue1DequeuedTransactionsTotal   int64          `json:"queue1DequeuedTransactionsTotal"`
	Queue1InputBatchesPerSecond       float64        `json:"queue1InputBatchesPerSecond"`
	Queue1InputTransactionsPerSecond  float64        `json:"queue1InputTransactionsPerSecond"`
	Queue1OutputBatchesPerSecond      float64        `json:"queue1OutputBatchesPerSecond"`
	Queue1OutputTransactionsPerSecond float64        `json:"queue1OutputTransactionsPerSecond"`
	Policy                            policySnapshot `json:"policy"`
}

func snapshotHandler(requests chan<- request) http.Handler {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		reply := make(chan statusSnapshot, 1)
		requests <- request{
			kind:          getSnapshot,
			snapshotReply: reply,
		}
		snapshot := <-reply
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(snapshot)
		if err != nil {
			log.Printf("encode snapshot: %v", err)
			return
		}
	}
	return http.HandlerFunc(handler)
}
