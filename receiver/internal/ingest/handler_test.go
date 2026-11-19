package ingest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"testing/synctest"
	"time"
)

func TestConfigResolvesOptionalResponseStatus(t *testing.T) {
	tests := []struct {
		name    string
		input   *int
		want    int
		wantErr bool
	}{
		{name: "absent", want: http.StatusNoContent},
		{name: "explicit zero", input: new(0), wantErr: true},
		{name: "success", input: new(204), want: http.StatusNoContent},
		{name: "bad request", input: new(400), want: http.StatusBadRequest},
		{name: "server error", input: new(500), want: http.StatusInternalServerError},
		{name: "unsupported", input: new(201), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := Config{Path: "/internal/ingest", MaxBodyBytes: 1024, ResponseStatus: test.input}
			if err := config.Validate(); (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %t", err, test.wantErr)
			}
			server, err := NewHandler(config)
			if test.wantErr {
				if err == nil {
					t.Fatal("unsupported programmatic response status accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			assertStatus(t, server, 0, 0, false, 0, test.want)
			if test.input != nil {
				*test.input = 201
			}
			recorder := performRequest(
				server,
				httpRequest(http.MethodPost, "/internal/ingest", "application/json", validBatch()),
			)
			if recorder.Code != test.want {
				t.Fatalf("ingest status = %d, want %d", recorder.Code, test.want)
			}
			var accepted int64
			if test.want == http.StatusNoContent {
				accepted = 1
			}
			assertStatus(t, server, accepted, accepted, accepted != 0, 0, test.want)
		})
	}
}

func TestHandlerExactConfiguredPaths(t *testing.T) {
	for _, test := range []struct{ path, other string }{
		{path: "/v1/batches", other: "/v1/batches/extra"},
		{path: "/internal/ingest", other: "/internal/ingest-extra"},
		{path: "/AZaz09._~-/.hidden/...", other: "/AZaz09._~-/.hidden/...."},
		{path: "/status/batches", other: "/status/batches/extra"},
	} {
		t.Run(test.path, func(t *testing.T) {
			handler, err := NewHandler(Config{Path: test.path, MaxBodyBytes: 1024})
			if err != nil {
				t.Fatal(err)
			}
			if rec := performRequest(handler, httpRequest(http.MethodPost, test.path, "application/json", `[{}]`)); rec.Code != http.StatusNoContent {
				t.Fatalf("configured exact path status=%d", rec.Code)
			}
			if rec := performRequest(handler, httpRequest(http.MethodPost, test.other, "application/json", `[{}]`)); rec.Code != http.StatusNotFound {
				t.Fatalf("different path status=%d", rec.Code)
			}
			assertStatus(t, handler, 1, 1, true, 0, http.StatusNoContent)
		})
	}
}

func TestHandlerHEADAndMethodResponses(t *testing.T) {
	handler := newTestHandler(t, 1024, Config{})
	server := httptest.NewServer(handler)
	defer server.Close()
	for _, test := range []struct {
		method, path, allow string
		status              int
	}{
		{method: http.MethodHead, path: "/status", status: http.StatusOK},
		{method: http.MethodPost, path: "/status", status: http.StatusMethodNotAllowed, allow: "GET, HEAD"},
		{method: http.MethodGet, path: "/internal/ingest", status: http.StatusMethodNotAllowed, allow: "POST"},
		{method: http.MethodHead, path: "/internal/ingest", status: http.StatusMethodNotAllowed, allow: "POST"},
		{method: http.MethodGet, path: "/status/extra", status: http.StatusNotFound},
	} {
		t.Run(test.method+test.path, func(t *testing.T) {
			request, err := http.NewRequest(test.method, server.URL+test.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			closeErr := response.Body.Close()
			if err != nil || closeErr != nil || response.StatusCode != test.status || response.Header.Get("Allow") != test.allow {
				t.Fatalf("status=%d allow=%q read=%v close=%v", response.StatusCode, response.Header.Get("Allow"), err, closeErr)
			}
			if test.method == http.MethodHead && len(body) != 0 {
				t.Fatalf("HEAD body=%q", body)
			}
			if test.method != http.MethodHead && test.status == http.StatusMethodNotAllowed && string(body) != "Method Not Allowed\n" {
				t.Fatalf("405 body=%q", body)
			}
		})
	}
	assertStatus(t, handler, 0, 0, false, 0, http.StatusNoContent)
}

func TestHandlerStatusWireShape(t *testing.T) {
	handler := newTestHandler(t, 1024, Config{})
	recorder := performRequest(handler, httpRequest(http.MethodGet, "/status", "", ""))
	if recorder.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status Content-Type=%q", recorder.Header().Get("Content-Type"))
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &root); err != nil {
		t.Fatal(err)
	}
	keys := []string{"startedAt", "acceptedBatchesTotal", "acceptedTransactionsTotal", "lastAcceptedAt", "lab"}
	if len(root) != len(keys) {
		t.Fatalf("status keys=%v", root)
	}
	for _, key := range keys {
		if _, ok := root[key]; !ok {
			t.Fatalf("missing status field %s", key)
		}
	}
	var lab map[string]json.RawMessage
	if err := json.Unmarshal(root["lab"], &lab); err != nil {
		t.Fatal(err)
	}
	if len(lab) != 2 || string(lab["responseDelayMs"]) != "0" || string(lab["responseStatus"]) != "204" || string(root["lastAcceptedAt"]) != "null" {
		t.Fatalf("status shape changed: %s", recorder.Body.Bytes())
	}
	var started time.Time
	if err := json.Unmarshal(root["startedAt"], &started); err != nil || started.IsZero() {
		t.Fatalf("startedAt=%s error=%v", root["startedAt"], err)
	}
}

func TestHandlerIngest(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		contentType string
		body        string
		maxBody     int64
		wantStatus  int
		wantAllow   string
	}{
		{name: "accepts empty transaction object", method: http.MethodPost, contentType: "application/json", body: `[{}]`, maxBody: 1024, wantStatus: http.StatusNoContent},
		{name: "accepts unknown fields", method: http.MethodPost, contentType: "application/json", body: `[{"extra":true}]`, maxBody: 1024, wantStatus: http.StatusNoContent},
		{name: "rejects null element", method: http.MethodPost, contentType: "application/json", body: `[null]`, maxBody: 1024, wantStatus: http.StatusBadRequest},
		{name: "rejects mixed null element", method: http.MethodPost, contentType: "application/json", body: `[{},null]`, maxBody: 1024, wantStatus: http.StatusBadRequest},
		{name: "oversized malformed JSON", method: http.MethodPost, contentType: "application/json", body: "!" + strings.Repeat("x", 1024), maxBody: 1024, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "MIME checked before oversized body", method: http.MethodPost, contentType: "text/plain", body: strings.Repeat("x", 1025), maxBody: 1024, wantStatus: http.StatusBadRequest},
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
			server := newTestHandler(t, test.maxBody, Config{})
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
			var accepted int64
			if test.wantStatus == http.StatusNoContent {
				accepted = 1
			}
			assertStatus(t, server, accepted, accepted, accepted != 0, 0, http.StatusNoContent)
		})
	}
}

func TestHandlerLabModes(t *testing.T) {
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
			synctest.Test(t, func(t *testing.T) {
				server := newTestHandler(t, test.maxBody, Config{
					ResponseDelay:  250 * time.Millisecond,
					ResponseStatus: new(http.StatusInternalServerError),
				})

				startedAt := time.Now()
				recorder := performRequest(
					server,
					httpRequest(test.method, "/internal/ingest", test.contentType, test.body),
				)
				if elapsed := time.Since(startedAt); elapsed != 0 {
					t.Fatalf("invalid request took %s, want zero virtual delay", elapsed)
				}
				if recorder.Code != test.wantStatus {
					t.Fatalf("status = %d, want %d", recorder.Code, test.wantStatus)
				}
				if allow := recorder.Header().Get("Allow"); allow != test.wantAllow {
					t.Fatalf("Allow = %q, want %q", allow, test.wantAllow)
				}
				assertStatus(t, server, 0, 0, false, 250, http.StatusInternalServerError)
			})
		})
	}

	t.Run("delays validated success before acknowledgement", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			handler := newTestHandler(t, 1024, Config{ResponseDelay: 25 * time.Millisecond, ResponseStatus: new(http.StatusNoContent)})
			returned := make(chan *httptest.ResponseRecorder, 1)
			started := time.Now()
			go func() {
				returned <- performRequest(handler, httpRequest(http.MethodPost, "/internal/ingest", "application/json", validBatch()))
			}()
			synctest.Wait()
			time.Sleep(25*time.Millisecond - time.Nanosecond)
			synctest.Wait()
			select {
			case <-returned:
				t.Fatal("acknowledged before delay elapsed")
			default:
			}
			assertStatus(t, handler, 0, 0, false, 25, http.StatusNoContent)
			time.Sleep(time.Nanosecond)
			recorder := <-returned
			if recorder.Code != http.StatusNoContent || time.Since(started) != 25*time.Millisecond {
				t.Fatalf("status=%d elapsed=%s", recorder.Code, time.Since(started))
			}
			assertStatus(t, handler, 1, 1, true, 25, http.StatusNoContent)
		})
	})

	for _, when := range []string{"already canceled", "during delay"} {
		t.Run(when, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				handler := newTestHandler(t, 1024, Config{ResponseDelay: time.Second})
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if when == "already canceled" {
					cancel()
				}
				returned := make(chan struct{})
				started := time.Now()
				go func() {
					performRequest(handler, httpRequestWithContext(ctx, http.MethodPost, "/internal/ingest", "application/json", validBatch()))
					close(returned)
				}()
				synctest.Wait()
				wantElapsed := time.Duration(0)
				if when == "during delay" {
					time.Sleep(100 * time.Millisecond)
					wantElapsed = 100 * time.Millisecond
					cancel()
				}
				<-returned
				if time.Since(started) != wantElapsed {
					t.Fatalf("cancel delay=%s,want %s", time.Since(started), wantElapsed)
				}
				assertStatus(t, handler, 0, 0, false, 1000, http.StatusNoContent)
			})
		})
	}
}

func TestHandlerIngestMultiTransactionBatch(t *testing.T) {
	server := newTestHandler(t, 2048, Config{})

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

func TestHandlerReadFailurePrecedesLabSettings(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		handler := newTestHandler(t, 1024, Config{ResponseDelay: 250 * time.Millisecond, ResponseStatus: new(500)})
		request := httpRequest(http.MethodPost, "/internal/ingest", "application/json", "")
		request.Body = io.NopCloser(iotest.ErrReader(io.ErrUnexpectedEOF))
		started := time.Now()
		recorder := performRequest(handler, request)
		if recorder.Code != http.StatusBadRequest || time.Since(started) != 0 {
			t.Fatalf("read failure status=%d elapsed=%s", recorder.Code, time.Since(started))
		}
		assertStatus(t, handler, 0, 0, false, 250, http.StatusInternalServerError)
	})
}

func TestHandlerCanceledRequestWithZeroDelayDoesNotAcknowledge(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		handler := newTestHandler(t, 1024, Config{})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		started := time.Now()
		recorder := performRequest(handler, httpRequestWithContext(ctx, http.MethodPost, "/internal/ingest", "application/json", validBatch()))
		if recorder.Code == http.StatusNoContent || time.Since(started) != 0 {
			t.Fatalf("canceled request status=%d elapsed=%s", recorder.Code, time.Since(started))
		}
		assertStatus(t, handler, 0, 0, false, 0, http.StatusNoContent)
	})
}

func TestHandlerIngestConcurrent(t *testing.T) {
	const requestCount = 16

	server := newTestHandler(t, 1024, Config{})
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

func newTestHandler(t *testing.T, maxBodyBytes int64, config Config) *Handler {
	t.Helper()

	config.Path = "/internal/ingest"
	config.MaxBodyBytes = maxBodyBytes
	server, err := NewHandler(config)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
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

func performRequest(server *Handler, request *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

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

func assertStatus(t *testing.T, server *Handler, wantBatches, wantTransactions int64, wantLastAccepted bool, wantDelayMS, wantStatus int) {
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
