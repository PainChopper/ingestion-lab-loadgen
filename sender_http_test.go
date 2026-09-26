package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestSenderHTTPAttemptPostsJSONBatch(t *testing.T) {
	batch := []Transaction{{ClientID: "client-1"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/internal/test/ingest" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		if string(body) != "[{\"client_id\":\"client-1\",\"event_time\":\"0001-01-01T00:00:00Z\",\"amount\":0,\"event_type\":0,\"event_subtype\":0,\"currency\":0,\"src_type11\":0,\"src_type12\":0,\"dst_type11\":0,\"dst_type12\":0,\"src_type21\":0,\"src_type22\":0,\"src_type31\":0,\"src_type32\":0,\"fold\":0}]" {
			t.Fatalf("JSON body = %s", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	attempt := newSenderHTTPAttempt(server.URL+"/internal/test/ingest", server.Client())
	if got := attempt.deliver(context.Background(), batch, 0, 1); got != senderAttemptSuccess {
		t.Fatalf("deliver outcome = %d, want success", got)
	}
}

func TestSenderHTTPAttemptClassifiesResponseStatuses(t *testing.T) {
	tests := []struct {
		status int
		want   senderAttemptOutcome
	}{
		{status: http.StatusNoContent, want: senderAttemptSuccess},
		{status: http.StatusBadRequest, want: senderAttemptTerminalFailure},
		{status: http.StatusMethodNotAllowed, want: senderAttemptTerminalFailure},
		{status: http.StatusRequestEntityTooLarge, want: senderAttemptTerminalFailure},
		{status: http.StatusOK, want: senderAttemptRetryableFailure},
		{status: http.StatusInternalServerError, want: senderAttemptRetryableFailure},
	}
	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))
			defer server.Close()

			attempt := newSenderHTTPAttempt(server.URL, server.Client())
			if got := attempt.deliver(context.Background(), []Transaction{{ClientID: "client"}}, 0, 1); got != test.want {
				t.Fatalf("deliver outcome = %d, want %d", got, test.want)
			}
		})
	}
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
			var channel channelTelemetry
			var telemetry senderTelemetry
			var consumed atomic.Int64
			policy := testPolicy(t).Sender
			policy.API.URL = server.URL
			pool := startSenderPool(batches, &channel, &telemetry, &consumed, 1, policy.API, policy.Retry)
			pool.wait = func(context.Context, time.Duration) bool { return true }
			batches <- []Transaction{{ClientID: "invalid"}}
			waitForSenderCondition(t, func() bool { return consumed.Load() == 1 })
			<-pool.stop()
			if requests.Load() != 4 {
				t.Fatalf("HTTP requests = %d, want 4", requests.Load())
			}
		})
	}
}

func TestSenderHTTPAttemptRetriesNetworkFailureAndClosesResponse(t *testing.T) {
	closed := atomic.Bool{}
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Body:       &closeTrackingBody{closed: &closed},
			Header:     make(http.Header),
		}, nil
	})}
	attempt := newSenderHTTPAttempt("http://example.test/ingest", client)
	if got := attempt.deliver(context.Background(), []Transaction{{ClientID: "client"}}, 0, 1); got != senderAttemptRetryableFailure {
		t.Fatalf("response outcome = %d, want retryable failure", got)
	}
	if !closed.Load() {
		t.Fatal("response body was not closed")
	}

	client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network failure")
	})
	if got := attempt.deliver(context.Background(), []Transaction{{ClientID: "client"}}, 0, 1); got != senderAttemptRetryableFailure {
		t.Fatalf("network outcome = %d, want retryable failure", got)
	}
}

func TestSenderHTTPAttemptStopsOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("request should be canceled")
	})}
	attempt := newSenderHTTPAttempt("http://example.test/ingest", client)
	if got := attempt.deliver(ctx, []Transaction{{ClientID: "client"}}, 0, 1); got != senderAttemptCanceled {
		t.Fatalf("canceled outcome = %d, want canceled", got)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type closeTrackingBody struct {
	closed *atomic.Bool
}

func (*closeTrackingBody) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (b *closeTrackingBody) Close() error {
	b.closed.Store(true)
	return nil
}
