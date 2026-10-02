package main

import (
	"encoding/json"
	"net/http"

	"go.uber.org/zap"
)

func snapshotHandler(requests chan<- runtimeCommand, logger *zap.Logger) http.Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		status := requestRuntimeStatus(requests)
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(status)
		if err != nil {
			logger.Error("snapshot response encoding failed", zap.String("event", "run_failed"))
			return
		}
	}
	return http.HandlerFunc(handler)
}
