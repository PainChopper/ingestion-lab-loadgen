package main

import (
	"encoding/json"
	"log"
	"net/http"
)

type statusSnapshot struct {
	Run           runSnapshot       `json:"run"`
	Reader        readerSnapshot    `json:"reader"`
	Throttler     throttlerSnapshot `json:"throttler"`
	Sender        senderSnapshot    `json:"sender"`
	ReaderChannel channelSnapshot   `json:"readerChannel"`
	SenderChannel channelSnapshot   `json:"senderChannel"`
	Policy        policySnapshot    `json:"policy"`
}

type runSnapshot struct {
	State             runState `json:"state"`
	TotalTransactions int64    `json:"totalTransactions"`
	ElapsedMs         int64    `json:"elapsedMs"`
	StartError        *string  `json:"startError"`
}

type readerSnapshot struct {
	Workers       int     `json:"workers"`
	ReadBatchSize int     `json:"readBatchSize"`
	ReadTps       float64 `json:"readTps"`
	RowsRead      int64   `json:"rowsRead"`
	Source        *string `json:"source"`
}

type throttlerSnapshot struct {
	RequestedTps     int     `json:"requestedTps"`
	AdmittedTps      float64 `json:"admittedTps"`
	InstallationMode string  `json:"installationMode"`
}

type senderSnapshot struct {
	Workers                   int                `json:"workers"`
	LiveWorkers               int                `json:"liveWorkers"`
	DrainingWorkers           int                `json:"drainingWorkers"`
	WorkerSlots               []senderWorkerSlot `json:"workerSlots"`
	SimulatedDelayMS          int                `json:"simulatedDelayMs"`
	SimulatedErrorRatePercent int                `json:"simulatedErrorRatePercent"`
}

type channelSnapshot struct {
	Capacity                      int     `json:"capacity"`
	DepthBatches                  int     `json:"depthBatches"`
	BufferedTransactions          int     `json:"bufferedTransactions"`
	BlockedSenders                int     `json:"blockedSenders"`
	OldestBlockedSenderMs         int64   `json:"oldestBlockedSenderMs"`
	BlockedMs                     int64   `json:"blockedMs"`
	SentBatchesTotal              int64   `json:"sentBatchesTotal"`
	SentTransactionsTotal         int64   `json:"sentTransactionsTotal"`
	ReceivedBatchesTotal          int64   `json:"receivedBatchesTotal"`
	ReceivedTransactionsTotal     int64   `json:"receivedTransactionsTotal"`
	SentBatchesPerSecond          float64 `json:"inputBatchesPerSecond"`
	SentTransactionsPerSecond     float64 `json:"inputTransactionsPerSecond"`
	ReceivedBatchesPerSecond      float64 `json:"outputBatchesPerSecond"`
	ReceivedTransactionsPerSecond float64 `json:"outputTransactionsPerSecond"`
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
