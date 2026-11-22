package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestStartHTTPServerLogsActualListenerAddress(t *testing.T) {
	// Ask the OS to assign a port to distinguish the actual and configured addresses.
	const configuredAddr = "127.0.0.1:0"
	core, logs := observer.New(zap.InfoLevel)
	server, done, err := Start(configuredAddr, http.NotFoundHandler(), zap.New(core))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := Shutdown(ctx, server); err != nil {
			t.Error(err)
		}
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	entries := logs.FilterMessage("service started").All()
	if len(entries) != 1 {
		t.Fatalf("service start logs=%d", len(entries))
	}
	fields := entries[0].ContextMap()
	addr, ok := fields["http_addr"].(string)
	if !ok || addr == configuredAddr || fields["event"] != "service_started" {
		t.Fatalf("start fields=%v", fields)
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		t.Fatal(err)
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
	logger := zap.NewNop()
	handler := newTestMux(t, testRuntime{}, testStartupConfig(t), logger)
	server, done, err := Start(addr, handler, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := Shutdown(ctx, server); err != nil {
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
	response, err := client.Get("http://" + addr + api.CommandsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close response body: %v", err)
		}
	}()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("configured server GET = %d, want %d", response.StatusCode, http.StatusMethodNotAllowed)
	}
	t.Logf("configured server %s answered GET %s with %d", addr, api.CommandsPath, response.StatusCode)
}

func TestStartHTTPServerReturnsOccupiedAddressError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close listener: %v", err)
		}
	})
	addr := listener.Addr().String()
	server, done, err := Start(addr, http.NotFoundHandler(), zap.NewNop())
	if server != nil || done != nil {
		t.Fatal("failed startup returned server or done channel")
	}
	var listenError *net.OpError
	if !errors.As(err, &listenError) || listenError.Op != "listen" {
		t.Fatalf("startup error = %v, want wrapped listen error", err)
	}
	t.Logf("occupied address %s returned %v", addr, err)
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
	if err := Shutdown(shutdownCtx, server.Config); err != nil {
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
