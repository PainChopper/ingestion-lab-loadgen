package main

import (
	"encoding/json"
	"log"
	"net/http"
)

type statusSnapshot struct {
	RunState          runState `json:"runState"`
	TotalTransactions int64    `json:"totalTransactions"`
	ReaderWorkers     int      `json:"readerWorkers"`
	SenderWorkers     int      `json:"senderWorkers"`
}

func snapshotHandler(commands chan<- request) http.Handler {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		reply := make(chan statusSnapshot, 1)
		commands <- request{
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
