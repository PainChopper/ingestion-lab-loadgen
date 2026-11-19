package ingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"sync"
	"time"
)

// Handler serves the exact ingest route and the receiver status endpoint.
type Handler struct {
	config         Config
	responseStatus int
	handler        http.Handler
	state          handlerState
}

type handlerState struct {
	mu                        sync.RWMutex
	startedAt                 time.Time
	acceptedBatchesTotal      int64
	acceptedTransactionsTotal int64
	lastAcceptedAt            *time.Time
}

type transaction struct {
	ClientID     string    `json:"client_id"`
	EventTime    time.Time `json:"event_time"`
	Amount       float32   `json:"amount"`
	EventType    int32     `json:"event_type"`
	EventSubtype int32     `json:"event_subtype"`
	Currency     int32     `json:"currency"`
	SrcType11    int32     `json:"src_type11"`
	SrcType12    int32     `json:"src_type12"`
	DstType11    int32     `json:"dst_type11"`
	DstType12    int32     `json:"dst_type12"`
	SrcType21    int32     `json:"src_type21"`
	SrcType22    int32     `json:"src_type22"`
	SrcType31    int32     `json:"src_type31"`
	SrcType32    int32     `json:"src_type32"`
	Fold         int32     `json:"fold"`
}

type statusResponse struct {
	StartedAt                 time.Time         `json:"startedAt"`
	AcceptedBatchesTotal      int64             `json:"acceptedBatchesTotal"`
	AcceptedTransactionsTotal int64             `json:"acceptedTransactionsTotal"`
	LastAcceptedAt            *time.Time        `json:"lastAcceptedAt"`
	Lab                       labStatusResponse `json:"lab"`
}

type labStatusResponse struct {
	ResponseDelayMS int64 `json:"responseDelayMs"`
	ResponseStatus  int   `json:"responseStatus"`
}

func NewHandler(config Config) (*Handler, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	handler := &Handler{
		config:         config,
		responseStatus: config.responseStatus(),
		state: handlerState{
			startedAt: time.Now().UTC(),
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+config.Path, handler.handleIngest)
	mux.HandleFunc("GET /status", handler.handleStatus)
	handler.handler = mux

	return handler, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.handler.ServeHTTP(w, r) }

func (h *Handler) handleIngest(writer http.ResponseWriter, request *http.Request) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(writer, "bad request", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, h.config.MaxBodyBytes))
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			http.Error(writer, "payload too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(writer, "bad request", http.StatusBadRequest)
		}
		return
	}
	var transactions []transaction
	if err := json.Unmarshal(body, &transactions); err != nil || len(transactions) == 0 {
		http.Error(writer, "bad request", http.StatusBadRequest)
		return
	}

	if !waitForDelay(request, h.config.ResponseDelay) {
		return
	}
	if h.responseStatus != http.StatusNoContent {
		writer.WriteHeader(h.responseStatus)
		return
	}

	h.state.accept(len(transactions))
	writer.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleStatus(writer http.ResponseWriter, request *http.Request) {
	status := h.state.snapshot(h.config.ResponseDelay, h.responseStatus)
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(status)
}

func waitForDelay(request *http.Request, delay time.Duration) bool {
	if delay == 0 {
		return request.Context().Err() == nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return request.Context().Err() == nil
	case <-request.Context().Done():
		return false
	}
}

func (s *handlerState) accept(transactionCount int) {
	acceptedAt := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.acceptedBatchesTotal++
	s.acceptedTransactionsTotal += int64(transactionCount)
	s.lastAcceptedAt = &acceptedAt
}

func (s *handlerState) snapshot(responseDelay time.Duration, responseStatus int) statusResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var lastAcceptedAt *time.Time
	if s.lastAcceptedAt != nil {
		value := *s.lastAcceptedAt
		lastAcceptedAt = &value
	}

	return statusResponse{
		StartedAt:                 s.startedAt,
		AcceptedBatchesTotal:      s.acceptedBatchesTotal,
		AcceptedTransactionsTotal: s.acceptedTransactionsTotal,
		LastAcceptedAt:            lastAcceptedAt,
		Lab: labStatusResponse{
			ResponseDelayMS: responseDelay.Milliseconds(),
			ResponseStatus:  responseStatus,
		},
	}
}

func (t *transaction) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("transaction must not be null")
	}
	type transactionValue transaction
	return json.Unmarshal(data, (*transactionValue)(t))
}
