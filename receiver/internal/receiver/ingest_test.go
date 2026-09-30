package receiver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestServer_Ingest(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		contentType string
		body        string
		maxBody     int64
		wantStatus  int
		wantAllow   string
	}{
		{
			name:        "accepts non empty transaction array",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        validBatch(),
			maxBody:     1024,
			wantStatus:  http.StatusNoContent,
		},
		{
			name:        "accepts content type parameters",
			method:      http.MethodPost,
			contentType: "application/json; charset=utf-8",
			body:        validBatch(),
			maxBody:     1024,
			wantStatus:  http.StatusNoContent,
		},
		{
			name:        "rejects wrong method with allow post",
			method:      http.MethodGet,
			contentType: "application/json",
			body:        validBatch(),
			maxBody:     1024,
			wantStatus:  http.StatusMethodNotAllowed,
			wantAllow:   http.MethodPost,
		},
		{
			name:       "rejects missing content type",
			method:     http.MethodPost,
			body:       validBatch(),
			maxBody:    1024,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:        "rejects non json content type",
			method:      http.MethodPost,
			contentType: "text/plain",
			body:        validBatch(),
			maxBody:     1024,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "rejects malformed json",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        "[",
			maxBody:     1024,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "rejects empty body",
			method:      http.MethodPost,
			contentType: "application/json",
			maxBody:     1024,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "rejects empty array",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        "[]",
			maxBody:     1024,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "rejects non array document",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        "{}",
			maxBody:     1024,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "rejects second json document",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        validBatch() + " []",
			maxBody:     1024,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "accepts body at configured limit",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        validBatch() + " ",
			maxBody:     int64(len(validBatch()) + 1),
			wantStatus:  http.StatusNoContent,
		},
		{
			name:        "rejects body one byte over configured limit",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        validBatch() + " ",
			maxBody:     int64(len(validBatch())),
			wantStatus:  http.StatusRequestEntityTooLarge,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newTestServer(t, test.maxBody, LabConfig{})
			recorder := performRequest(
				server,
				httpRequest(test.method, "/internal/ingest", test.contentType, test.body),
			)

			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
			if allow := recorder.Header().Get("Allow"); allow != test.wantAllow {
				t.Fatalf("Allow = %q, want %q", allow, test.wantAllow)
			}
			if test.wantStatus == http.StatusNoContent && recorder.Body.Len() != 0 {
				t.Fatalf("204 response body length = %d, want 0", recorder.Body.Len())
			}
		})
	}
}

func TestServer_LabModes(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		contentType string
		body        string
		maxBody     int64
		wantStatus  int
		wantAllow   string
	}{
		{
			name:        "invalid method keeps method validation status",
			method:      http.MethodGet,
			contentType: "application/json",
			body:        validBatch(),
			maxBody:     1024,
			wantStatus:  http.StatusMethodNotAllowed,
			wantAllow:   http.MethodPost,
		},
		{
			name:       "missing content type keeps content type validation status",
			method:     http.MethodPost,
			body:       validBatch(),
			maxBody:    1024,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:        "wrong content type keeps content type validation status",
			method:      http.MethodPost,
			contentType: "text/plain",
			body:        validBatch(),
			maxBody:     1024,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "malformed json keeps json validation status",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        "[",
			maxBody:     1024,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "null element keeps json validation status",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        "[null]",
			maxBody:     1024,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "body over limit keeps body validation status",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        validBatch() + " ",
			maxBody:     int64(len(validBatch())),
			wantStatus:  http.StatusRequestEntityTooLarge,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newTestServer(t, test.maxBody, LabConfig{
				ResponseDelay:  250 * time.Millisecond,
				ResponseStatus: http.StatusInternalServerError,
			})

			startedAt := time.Now()
			recorder := performRequest(
				server,
				httpRequest(test.method, "/internal/ingest", test.contentType, test.body),
			)
			if elapsed := time.Since(startedAt); elapsed >= 100*time.Millisecond {
				t.Fatalf("invalid request took %s, want less than 100ms", elapsed)
			}
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
			if allow := recorder.Header().Get("Allow"); allow != test.wantAllow {
				t.Fatalf("Allow = %q, want %q", allow, test.wantAllow)
			}
			assertStatus(t, server, 0, 0, false, 250, http.StatusInternalServerError)
		})
	}

	t.Run("delays validated success before acknowledgement", func(t *testing.T) {
		server := newTestServer(t, 1024, LabConfig{ResponseDelay: 25 * time.Millisecond, ResponseStatus: http.StatusNoContent})

		startedAt := time.Now()
		recorder := performRequest(server, httpRequest(http.MethodPost, "/internal/ingest", "application/json", validBatch()))
		elapsed := time.Since(startedAt)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", recorder.Code)
		}
		if elapsed < 20*time.Millisecond {
			t.Fatalf("response delay = %s, want at least 20ms", elapsed)
		}
		assertStatus(t, server, 1, 1, true, 25, http.StatusNoContent)
	})

	t.Run("forced status does not acknowledge batch", func(t *testing.T) {
		server := newTestServer(t, 1024, LabConfig{ResponseStatus: http.StatusInternalServerError})

		recorder := performRequest(server, httpRequest(http.MethodPost, "/internal/ingest", "application/json", validBatch()))
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", recorder.Code)
		}
		assertStatus(t, server, 0, 0, false, 0, http.StatusInternalServerError)
	})

	t.Run("canceled request stops waiting for lab delay", func(t *testing.T) {
		server := newTestServer(t, 1024, LabConfig{ResponseDelay: time.Second, ResponseStatus: http.StatusNoContent})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		startedAt := time.Now()
		performRequest(server, httpRequestWithContext(ctx, http.MethodPost, "/internal/ingest", "application/json", validBatch()))
		if elapsed := time.Since(startedAt); elapsed >= 100*time.Millisecond {
			t.Fatalf("canceled delay took %s, want less than 100ms", elapsed)
		}
		assertStatus(t, server, 0, 0, false, 1000, http.StatusNoContent)
	})
}

func TestServer_IngestMultiTransactionBatch(t *testing.T) {
	server := newTestServer(t, 2048, LabConfig{})

	recorder := performRequest(server, httpRequest(
		http.MethodPost,
		"/internal/ingest",
		"application/json",
		validMultiTransactionBatch(),
	))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", recorder.Code)
	}
	assertStatus(t, server, 1, 2, true, 0, http.StatusNoContent)
}

func TestServer_IngestConcurrent(t *testing.T) {
	const requestCount = 16

	server := newTestServer(t, 1024, LabConfig{})
	statuses := make(chan int, requestCount)

	var requests sync.WaitGroup
	for range requestCount {
		requests.Add(1)
		go func() {
			defer requests.Done()

			recorder := performRequest(
				server,
				httpRequest(http.MethodPost, "/internal/ingest", "application/json", validBatch()),
			)
			statuses <- recorder.Code
		}()
	}
	requests.Wait()
	close(statuses)

	for status := range statuses {
		if status != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", status)
		}
	}
	assertStatus(t, server, requestCount, requestCount, true, 0, http.StatusNoContent)
}

func newTestServer(t *testing.T, maxBodyBytes int64, lab LabConfig) *Server {
	t.Helper()

	server, err := NewServer(Config{
		SchemaVersion: 1,
		Server: ServerConfig{
			Address:         "127.0.0.1:0",
			ShutdownTimeout: time.Second,
		},
		Ingest: IngestConfig{
			Path:         "/internal/ingest",
			MaxBodyBytes: maxBodyBytes,
		},
		Logging: LoggingConfig{Level: "info"},
		Lab:     lab,
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	return server
}

func httpRequest(method, path, contentType, body string) *http.Request {
	return httpRequestWithContext(context.Background(), method, path, contentType, body)
}

func httpRequestWithContext(ctx context.Context, method, path, contentType, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}

	return request
}

func performRequest(server *Server, request *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)

	return recorder
}

func validBatch() string {
	return `[{"client_id":"client-1","event_time":"2026-09-28T00:00:00Z","amount":1.5,"event_type":1,"event_subtype":2,"currency":643,"src_type11":3,"src_type12":4,"dst_type11":5,"dst_type12":6,"src_type21":7,"src_type22":8,"src_type31":9,"src_type32":10,"fold":11}]`
}

func validMultiTransactionBatch() string {
	transaction := strings.TrimSuffix(strings.TrimPrefix(validBatch(), "["), "]")
	secondTransaction := strings.Replace(transaction, `"client-1"`, `"client-2"`, 1)

	return "[" + transaction + "," + secondTransaction + "]"
}

func assertStatus(t *testing.T, server *Server, wantBatches, wantTransactions int64, wantLastAccepted bool, wantDelayMS, wantStatus int) {
	t.Helper()

	recorder := performRequest(server, httpRequest(http.MethodGet, "/status", "", ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status endpoint code = %d, want 200", recorder.Code)
	}

	var status struct {
		AcceptedBatchesTotal      int64      `json:"acceptedBatchesTotal"`
		AcceptedTransactionsTotal int64      `json:"acceptedTransactionsTotal"`
		LastAcceptedAt            *time.Time `json:"lastAcceptedAt"`
		Lab                       struct {
			ResponseDelayMS int `json:"responseDelayMs"`
			ResponseStatus  int `json:"responseStatus"`
		} `json:"lab"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.AcceptedBatchesTotal != wantBatches {
		t.Fatalf("acceptedBatchesTotal = %d, want %d", status.AcceptedBatchesTotal, wantBatches)
	}
	if status.AcceptedTransactionsTotal != wantTransactions {
		t.Fatalf("acceptedTransactionsTotal = %d, want %d", status.AcceptedTransactionsTotal, wantTransactions)
	}
	if got := status.LastAcceptedAt != nil; got != wantLastAccepted {
		t.Fatalf("lastAcceptedAt present = %t, want %t", got, wantLastAccepted)
	}
	if status.Lab.ResponseDelayMS != wantDelayMS {
		t.Fatalf("lab.responseDelayMs = %d, want %d", status.Lab.ResponseDelayMS, wantDelayMS)
	}
	if status.Lab.ResponseStatus != wantStatus {
		t.Fatalf("lab.responseStatus = %d, want %d", status.Lab.ResponseStatus, wantStatus)
	}
}
