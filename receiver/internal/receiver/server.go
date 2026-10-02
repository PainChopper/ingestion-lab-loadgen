package receiver

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"sync"
	"time"
)

type Server struct {
	config         Config
	responseStatus int
	handler        http.Handler
	state          serverState
}

type serverState struct {
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
	ResponseDelayMS int `json:"responseDelayMs"`
	ResponseStatus  int `json:"responseStatus"`
}

func NewServer(config Config) (*Server, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}

	server := &Server{
		config:         config,
		responseStatus: config.Lab.responseStatus(),
		state: serverState{
			startedAt: time.Now().UTC(),
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc(config.Ingest.Path, server.handleIngest)
	mux.HandleFunc("/status", server.handleStatus)
	server.handler = mux

	return server, nil
}

func (server *Server) Handler() http.Handler {
	return server.handler
}

func (server *Server) handleIngest(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(writer, "bad request", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(request.Body, server.config.Ingest.MaxBodyBytes+1))
	if err != nil {
		http.Error(writer, "bad request", http.StatusBadRequest)
		return
	}
	if int64(len(body)) > server.config.Ingest.MaxBodyBytes {
		http.Error(writer, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	transactions := []*transaction{}
	if err := decoder.Decode(&transactions); err != nil || len(transactions) == 0 {
		http.Error(writer, "bad request", http.StatusBadRequest)
		return
	}
	for _, transaction := range transactions {
		if transaction == nil {
			http.Error(writer, "bad request", http.StatusBadRequest)
			return
		}
	}

	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		http.Error(writer, "bad request", http.StatusBadRequest)
		return
	}

	if !waitForDelay(request, server.config.Lab.ResponseDelay) {
		return
	}
	if server.responseStatus != http.StatusNoContent {
		writer.WriteHeader(server.responseStatus)
		return
	}

	server.state.accept(len(transactions))
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) handleStatus(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	status := server.state.snapshot(server.config.Lab, server.responseStatus)
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(status); err != nil {
		return
	}
}

func waitForDelay(request *http.Request, delay time.Duration) bool {
	if delay == 0 {
		return request.Context().Err() == nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-request.Context().Done():
		return false
	}
}

func (state *serverState) accept(transactionCount int) {
	acceptedAt := time.Now().UTC()

	state.mu.Lock()
	defer state.mu.Unlock()

	state.acceptedBatchesTotal++
	state.acceptedTransactionsTotal += int64(transactionCount)
	state.lastAcceptedAt = &acceptedAt
}

func (state *serverState) snapshot(lab LabConfig, responseStatus int) statusResponse {
	state.mu.RLock()
	defer state.mu.RUnlock()

	var lastAcceptedAt *time.Time
	if state.lastAcceptedAt != nil {
		value := *state.lastAcceptedAt
		lastAcceptedAt = &value
	}

	return statusResponse{
		StartedAt:                 state.startedAt,
		AcceptedBatchesTotal:      state.acceptedBatchesTotal,
		AcceptedTransactionsTotal: state.acceptedTransactionsTotal,
		LastAcceptedAt:            lastAcceptedAt,
		Lab: labStatusResponse{
			ResponseDelayMS: int(lab.ResponseDelay / time.Millisecond),
			ResponseStatus:  responseStatus,
		},
	}
}
