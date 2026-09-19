package main

import (
	"encoding/json"
	"log"
	"net/http"
)

type statusSnapshot struct {
	RunState                                 runState       `json:"runState"`
	TotalTransactions                        int64          `json:"totalTransactions"`
	ReaderWorkers                            int            `json:"readerWorkers"`
	ReaderReadBatchSize                      int            `json:"readerReadBatchSize"`
	ThrottlerRequestedTPS                    int            `json:"throttlerRequestedTps"`
	ThrottlerInstallationMode                string         `json:"throttlerInstallationMode"`
	SenderWorkers                            int            `json:"senderWorkers"`
	ElapsedMs                                int64          `json:"elapsedMs"`
	StartError                               *string        `json:"startError"`
	ReaderReadTPS                            float64        `json:"readerReadTps"`
	ReaderRowsRead                           int64          `json:"readerRowsRead"`
	ReaderSource                             *string        `json:"readerSource"`
	ReaderChannelCapacity                    int            `json:"readerChannelCapacity"`
	ReaderChannelDepthBatches                int            `json:"readerChannelDepthBatches"`
	ReaderChannelBufferedTransactions        int            `json:"readerChannelBufferedTransactions"`
	ReaderChannelBlockedSenders              int            `json:"readerChannelBlockedSenders"`
	ReaderChannelOldestBlockedSenderMs       int64          `json:"readerChannelOldestBlockedSenderMs"`
	ReaderChannelBlockedMs                   int64          `json:"readerChannelBlockedMs"`
	ReaderChannelSentBatchesTotal            int64          `json:"readerChannelSentBatchesTotal"`
	ReaderChannelSentTransactionsTotal       int64          `json:"readerChannelSentTransactionsTotal"`
	ReaderChannelReceivedBatchesTotal        int64          `json:"readerChannelReceivedBatchesTotal"`
	ReaderChannelReceivedTransactionsTotal   int64          `json:"readerChannelReceivedTransactionsTotal"`
	ReaderChannelInputBatchesPerSecond       float64        `json:"readerChannelInputBatchesPerSecond"`
	ReaderChannelInputTransactionsPerSecond  float64        `json:"readerChannelInputTransactionsPerSecond"`
	ReaderChannelOutputBatchesPerSecond      float64        `json:"readerChannelOutputBatchesPerSecond"`
	ReaderChannelOutputTransactionsPerSecond float64        `json:"readerChannelOutputTransactionsPerSecond"`
	Policy                                   policySnapshot `json:"policy"`
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
