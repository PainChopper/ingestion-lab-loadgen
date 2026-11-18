package pipeline

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSenderClientPostsJSONBatch(t *testing.T) {
	batch := []Transaction{{ClientID: "client-1"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/internal/ingest" {
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

	attempt := newSenderClient(server.URL+"/internal/ingest", server.Client())
	if got := attempt.attempt(context.Background(), batch, 1); got != senderAttemptSuccess {
		t.Fatalf("deliver outcome = %d, want success", got)
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
	attempt := newSenderClient(server.URL+target, server.Client())
	if got := attempt.attempt(t.Context(), []Transaction{{ClientID: "client-1"}}, 1); got != senderAttemptSuccess {
		t.Fatalf("deliver outcome = %d, want success", got)
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
		status int
		want   senderAttemptOutcome
	}{
		{status: http.StatusNoContent, want: senderAttemptSuccess},
		{status: http.StatusBadRequest, want: senderAttemptFailure},
		{status: http.StatusMethodNotAllowed, want: senderAttemptFailure},
		{status: http.StatusRequestEntityTooLarge, want: senderAttemptFailure},
		{status: http.StatusOK, want: senderAttemptFailure},
		{status: http.StatusInternalServerError, want: senderAttemptFailure},
	}
	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))
			defer server.Close()

			attempt := newSenderClient(server.URL, server.Client())
			if got := attempt.attempt(context.Background(), []Transaction{{ClientID: "client"}}, 1); got != test.want {
				t.Fatalf("deliver outcome = %d, want %d", got, test.want)
			}
		})
	}
}

func TestSenderClientRetriesNetworkFailureAndClosesResponse(t *testing.T) {
	closed := atomic.Bool{}
	body := &closeTrackingBody{Reader: strings.NewReader(strings.Repeat("x", 128<<10)), closed: &closed}
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Body:       body,
			Header:     make(http.Header),
		}, nil
	})}
	attempt := newSenderClient("http://example.test/ingest", client)
	if got := attempt.attempt(context.Background(), []Transaction{{ClientID: "client"}}, 1); got != senderAttemptFailure {
		t.Fatalf("response outcome = %d, want failure", got)
	}
	if !closed.Load() {
		t.Fatal("response body was not closed")
	}
	if got := body.Len(); got != 64<<10 {
		t.Errorf("bytes left unread = %d, want %d", got, 64<<10)
	}

	client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network failure")
	})
	if got := attempt.attempt(context.Background(), []Transaction{{ClientID: "client"}}, 1); got != senderAttemptFailure {
		t.Fatalf("network outcome = %d, want failure", got)
	}
}

func TestSenderClientStopsOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("request should be canceled")
	})}
	attempt := newSenderClient("http://example.test/ingest", client)
	if got := attempt.attempt(ctx, []Transaction{{ClientID: "client"}}, 1); got != senderAttemptCanceled {
		t.Fatalf("canceled outcome = %d, want canceled", got)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type closeTrackingBody struct {
	*strings.Reader
	closed *atomic.Bool
}

func (b *closeTrackingBody) Close() error {
	b.closed.Store(true)
	return nil
}
