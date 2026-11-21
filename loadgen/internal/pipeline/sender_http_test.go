package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestSenderClientPostsJSONBatch(t *testing.T) {
	batch := []Transaction{{ClientID: "client-1"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
			http.Error(w, "unexpected method", http.StatusInternalServerError)
			return
		}
		if r.URL.Path != "/internal/ingest" {
			t.Errorf("path = %q", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusInternalServerError)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
			http.Error(w, "unexpected content type", http.StatusInternalServerError)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			http.Error(w, "read failed", http.StatusInternalServerError)
			return
		}
		if string(body) != "[{\"client_id\":\"client-1\",\"event_time\":\"0001-01-01T00:00:00Z\",\"amount\":0,\"event_type\":0,\"event_subtype\":0,\"currency\":0,\"src_type11\":0,\"src_type12\":0,\"dst_type11\":0,\"dst_type12\":0,\"src_type21\":0,\"src_type22\":0,\"src_type31\":0,\"src_type32\":0,\"fold\":0}]" {
			t.Errorf("JSON body = %s", body)
			http.Error(w, "unexpected body", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := newSenderClient(server.URL+"/internal/ingest", server.Client())
	if err := client.deliver(context.Background(), batch, 1); err != nil {
		t.Fatalf("deliver: %v", err)
	}
}

func TestSenderClientPreservesTargetQuery(t *testing.T) {
	const target = "/test/receive?source=loadgen&batch=1"
	observed := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- r.URL.RequestURI()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := newSenderClient(server.URL+target, server.Client())
	if err := client.deliver(t.Context(), []Transaction{{ClientID: "client-1"}}, 1); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	select {
	case got := <-observed:
		if got != target {
			t.Fatalf("Sender request URI = %q, want %q", got, target)
		}
	default:
		t.Fatal("target received no HTTP request")
	}
}

func TestSenderClientClassifiesResponseStatuses(t *testing.T) {
	tests := []struct {
		status  int
		wantErr bool
	}{
		{status: http.StatusNoContent, wantErr: false},
		{status: http.StatusBadRequest, wantErr: true},
		{status: http.StatusMethodNotAllowed, wantErr: true},
		{status: http.StatusRequestEntityTooLarge, wantErr: true},
		{status: http.StatusOK, wantErr: true},
		{status: http.StatusInternalServerError, wantErr: true},
	}
	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))
			defer server.Close()

			client := newSenderClient(server.URL, server.Client())
			if err := client.deliver(context.Background(), []Transaction{{ClientID: "client"}}, 1); (err != nil) != test.wantErr {
				t.Fatalf("deliver error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}

func TestSenderClientRejectsFailuresAndClosesBoundedResponse(t *testing.T) {
	closed := atomic.Bool{}
	body := &closeTrackingBody{Reader: strings.NewReader(strings.Repeat("x", 128<<10)), closed: &closed}
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Body:       body,
			Header:     make(http.Header),
		}, nil
	})}
	sender := newSenderClient("http://example.test/ingest", client)
	if err := sender.deliver(context.Background(), []Transaction{{ClientID: "client"}}, 1); err == nil {
		t.Fatal("HTTP error response accepted")
	}
	if !closed.Load() {
		t.Fatal("response body was not closed")
	}
	if got := body.Len(); got != 64<<10 {
		t.Errorf("bytes left unread = %d, want %d", got, 64<<10)
	}

	cause := errors.New("network failure")
	client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, cause
	})
	if err := sender.deliver(context.Background(), []Transaction{{ClientID: "client"}}, 1); !errors.Is(err, cause) {
		t.Fatalf("network error=%v, want preserved cause", err)
	}
}

func TestSenderClientRejectsInvalidPayloadAndRequest(t *testing.T) {
	for _, test := range []struct {
		name, url string
		amount    float32
	}{
		{name: "JSON marshal", url: "http://example.test/ingest", amount: float32(math.NaN())},
		{name: "invalid URL", url: "://invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, io.ErrUnexpectedEOF
			})}
			sender := newSenderClient(test.url, client)
			if err := sender.deliver(t.Context(), []Transaction{{Amount: test.amount}}, 1); err == nil || calls != 0 {
				t.Fatalf("error=%v transport calls=%d", err, calls)
			}
		})
	}
}

func TestSenderClientPreservesResponseReadFailureAndClosesBody(t *testing.T) {
	closed := atomic.Bool{}
	body := &closeTrackingBody{Reader: strings.NewReader(""), closed: &closed, readErr: io.ErrUnexpectedEOF}
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNoContent, Body: body, Header: make(http.Header)}, nil
	})}
	sender := newSenderClient("http://example.test/ingest", client)
	if err := sender.deliver(t.Context(), []Transaction{{}}, 1); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("response read error=%v, want preserved cause", err)
	}
	if !closed.Load() {
		t.Fatal("response body remained open after read failure")
	}
}

func TestSenderClientStopsOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return nil, request.Context().Err()
	})}
	sender := newSenderClient("http://example.test/ingest", client)
	if err := sender.deliver(ctx, []Transaction{{ClientID: "client"}}, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v, want context.Canceled", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type closeTrackingBody struct {
	*strings.Reader
	closed  *atomic.Bool
	readErr error
}

func (b *closeTrackingBody) Read(p []byte) (int, error) {
	if b.readErr != nil {
		return 0, b.readErr
	}
	return b.Reader.Read(p)
}

func (b *closeTrackingBody) Close() error {
	b.closed.Store(true)
	return nil
}

func TestSenderPoolRetriesHTTPInputErrorsUntilSuccess(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusMethodNotAllowed, http.StatusRequestEntityTooLarge} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if requests.Add(1) == 4 {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.WriteHeader(status)
			}))
			defer server.Close()

			batches := make(chan []Transaction)
			var channel batchQueue
			channel.ch = batches
			channel.batchSize = 0

			config := testSenderOptions()
			config.RetryDelays = []time.Duration{1 * time.Millisecond}
			config.SenderURL = server.URL
			config.Senders = 1
			config.Logger = zap.NewNop()
			pool := startSenderPool(context.Background(), &channel, config, nil)
			batches <- []Transaction{{ClientID: "invalid"}}
			close(batches)
			<-pool.done
			if got := pool.completed.Load(); got != 1 {
				t.Fatalf("completed=%d, want 1", got)
			}
			if requests.Load() != 4 {
				t.Fatalf("HTTP requests = %d, want 4", requests.Load())
			}
		})
	}
}

func TestSenderPoolReusesConnectionsAcrossConcurrentWaves(t *testing.T) {
	for _, requestsPerWave := range []int{4, 112} {
		t.Run(fmt.Sprintf("workers=%d", requestsPerWave), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			entered := make(chan struct{}, requestsPerWave)
			release := make(chan struct{})
			var connections atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					return
				}
				entered <- struct{}{}
				select {
				case <-release:
					_, _ = io.WriteString(w, "retry")
				case <-r.Context().Done():
				}
			}))
			server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					connections.Add(1)
				}
			}
			server.Start()
			defer server.Close()
			defer close(release)

			batches := make(chan []Transaction)
			var telemetry batchQueue
			telemetry.ch = batches
			telemetry.batchSize = 0

			config := testSenderOptions()
			config.SenderURL = server.URL
			config.SenderMaxConns = requestsPerWave
			config.Senders = requestsPerWave
			config.Logger = zap.NewNop()
			pool := startSenderPool(ctx, &telemetry, config, nil)
			defer func() {
				cancel()
				<-pool.done
			}()

			for wave := 1; wave <= 2; wave++ {
				results := make(chan error, requestsPerWave)
				for range requestsPerWave {
					go func() {
						results <- pool.deliver(ctx, []Transaction{{ClientID: "client"}}, 1)
					}()
				}
				for range requestsPerWave {
					select {
					case <-entered:
					case <-ctx.Done():
						t.Fatalf("wave %d did not reach %d simultaneous requests", wave, requestsPerWave)
					}
				}
				for range requestsPerWave {
					select {
					case release <- struct{}{}:
					case <-ctx.Done():
						t.Fatalf("wave %d did not release all responses", wave)
					}
				}
				for range requestsPerWave {
					select {
					case got := <-results:
						if got == nil {
							t.Errorf("wave %d error = %v, want failure for HTTP 200", wave, got)
						}
					case <-ctx.Done():
						t.Fatalf("wave %d did not finish reading all responses", wave)
					}
				}
				got := connections.Load()
				t.Logf("wave %d total TCP connections = %d", wave, got)
				if got != int32(requestsPerWave) {
					t.Fatalf("wave %d total new connections = %d, want %d", wave, got, requestsPerWave)
				}
			}
		})
	}
}

func TestSenderPoolEntersBackoffWhenReceiverTimesOut(t *testing.T) {
	entered := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	batches := make(chan []Transaction)
	var telemetry batchQueue
	telemetry.ch = batches
	telemetry.batchSize = 0

	config := testSenderOptions()
	config.SenderURL = server.URL
	config.SenderTimeout = 50 * time.Millisecond
	config.RetryDelays = []time.Duration{3_600_000 * time.Millisecond}
	backoff := make(chan struct{}, 1)
	config.RandomIntN = func(n int) int {
		// retry.delay runs after the worker enters backoff.
		backoff <- struct{}{}
		return n / 2
	}
	config.Senders = 1
	config.Logger = zap.NewNop()
	pool := startSenderPool(ctx, &telemetry, config, nil)
	defer func() {
		cancel()
		<-pool.done
	}()
	batches <- []Transaction{{ClientID: "client"}}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("receiver did not get the request")
	}
	select {
	case <-backoff:
	case <-ctx.Done():
		t.Fatal("Sender did not enter backoff after the HTTP timeout")
	}
	assertWorkerStateMultiset(t, projectWorkerStates(filterWorkerStates(pool.snapshot().WorkerStates, func(state SenderWorkerState) bool { return state.Activity == SenderBackoff }), func(state SenderWorkerState) SenderActivity { return state.Activity }), []SenderActivity{SenderBackoff})
}
