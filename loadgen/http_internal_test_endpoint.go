package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
)

const internalTestIngestMaxBodySize = 32 * 1024 * 1024

func internalTestIngestHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, internalTestIngestMaxBodySize+1))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if len(body) > internalTestIngestMaxBodySize {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			return
		}

		decoder := json.NewDecoder(bytes.NewReader(body))

		transactions := []Transaction{}
		if err := decoder.Decode(&transactions); err != nil || len(transactions) == 0 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		var extra json.RawMessage
		if err := decoder.Decode(&extra); err != io.EOF {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
}
