package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"go.uber.org/zap"
)

func snapshotHandler(requests runtimeRequests, logger *zap.Logger) http.Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		status, err := requestRuntimeStatus(r.Context(), requests)
		if err != nil {
			if errors.Is(err, errRuntimeUnavailable) {
				http.Error(w, "Runtime unavailable", http.StatusServiceUnavailable)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(status); err != nil {
			logger.Error("snapshot response encoding failed", zap.String("event", "run_failed"))
			return
		}
	}
	return http.HandlerFunc(handler)
}
