package main

import (
	"encoding/json"
	"net/http"

	"go.uber.org/zap"
)

func snapshotHandler(plane controlPlane, loggers ...*zap.Logger) http.Handler {
	logger := loggerOrNop(loggers)
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		snapshot := plane.snapshot()
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(snapshot)
		if err != nil {
			logger.Error("snapshot response encoding failed", zap.String("event", "run_failed"))
			return
		}
	}
	return http.HandlerFunc(handler)
}
