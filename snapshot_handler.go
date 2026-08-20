package main

import (
	"encoding/json"
	"log"
	"net/http"
)

func snapshotHandler(commands chan<- command) http.Handler {
	handler := func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		reply := make(chan statusSnapshot)
		commands <- command{
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
