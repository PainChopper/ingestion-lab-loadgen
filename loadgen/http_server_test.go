package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestServeMuxRoutes(t *testing.T) {
	loaded := testConfig(t)
	loaded.Sender.API.URL = "https://example.test" + snapshotPath
	mux := newServeMux(runtimeRequests{commands: make(chan runtimeCommand, 1)}, NewPrometheusMetrics(), loaded, zap.NewNop())

	for _, route := range []string{snapshotPath, commandsPath} {
		req := httptest.NewRequest(http.MethodGet, route, nil)
		_, pattern := mux.Handler(req)
		if pattern != route {
			t.Errorf("pattern = %q, want %q", pattern, route)
		}
	}
}

func TestServeMuxHasNoLocalIngest(t *testing.T) {
	mux := newServeMux(runtimeRequests{commands: make(chan runtimeCommand)}, NewPrometheusMetrics(), testConfig(t), zap.NewNop())
	for _, path := range []string{"/internal/test/ingest", "/internal/ingest"} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", path, recorder.Code)
		}
	}
}

func TestStartHTTPServerUsesConfiguredAddressAndShutsDown(t *testing.T) {
	available, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := available.Addr().String()
	if err := available.Close(); err != nil {
		t.Fatal(err)
	}
	loaded := testConfig(t)
	loaded.HTTP.Addr = addr
	server, done, err := startHTTPServer(runtimeRequests{commands: make(chan runtimeCommand)}, NewPrometheusMetrics(), loaded, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := shutdownHTTPServer(ctx, server); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("HTTP server did not exit")
		}
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatalf("configured address not released after shutdown: %v", err)
		}
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		t.Logf("shutdown released %s", addr)
	})
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + addr + commandsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("configured server GET = %d, want %d", response.StatusCode, http.StatusMethodNotAllowed)
	}
	t.Logf("configured server %s answered GET %s with %d", addr, commandsPath, response.StatusCode)
}

func TestStartHTTPServerReturnsOccupiedAddressError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	loaded := testConfig(t)
	loaded.HTTP.Addr = listener.Addr().String()
	server, done, err := startHTTPServer(runtimeRequests{commands: make(chan runtimeCommand)}, NewPrometheusMetrics(), loaded, zap.NewNop())
	if server != nil || done != nil {
		t.Fatal("failed startup returned server or done channel")
	}
	var listenError *net.OpError
	if !errors.As(err, &listenError) || listenError.Op != "listen" {
		t.Fatalf("startup error = %v, want wrapped listen error", err)
	}
	t.Logf("occupied address %s returned %v", loaded.HTTP.Addr, err)
}

func TestShutdownHTTPServerClosesAnActiveRequestAtDeadline(t *testing.T) {
	entered := make(chan struct{})
	left := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(left)
	}))
	server.Start()
	t.Cleanup(server.Close)
	requestCtx, cancelRequest := context.WithTimeout(t.Context(), time.Second)
	defer cancelRequest()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		response, err := server.Client().Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		result <- err
	}()
	select {
	case <-entered:
	case <-requestCtx.Done():
		t.Fatal("HTTP request did not enter handler")
	}
	shutdownCtx, cancelShutdown := context.WithDeadline(t.Context(), time.Now())
	defer cancelShutdown()
	if err := shutdownHTTPServer(shutdownCtx, server.Config); err != nil {
		t.Fatal(err)
	}
	select {
	case <-left:
	case <-requestCtx.Done():
		t.Fatal("HTTP handler remained active past force-close")
	}
	if err := <-result; err == nil || requestCtx.Err() != nil {
		t.Fatalf("request error = %v, client context = %v, want server-side close", err, requestCtx.Err())
	}
}
