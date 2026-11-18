package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestServeMuxHEADUsesSnapshotAndReturnsNoBody(t *testing.T) {
	requests := make(chan runtimeRequest)
	mux := newServeMux(runtimeRequests{queue: requests}, newPrometheusMetrics(), testConfig(t), zap.NewNop())
	server := httptest.NewServer(mux)
	defer server.Close()
	go func() {
		(<-requests).(snapshotRequest).reply <- runtimeSnapshot{Run: runSnapshot{State: runStateRunning}}
	}()
	for _, test := range []struct {
		path, allow string
		status      int
	}{
		{path: snapshotPath, status: http.StatusOK},
		{path: commandsPath, status: http.StatusMethodNotAllowed, allow: http.MethodPost},
	} {
		response, err := server.Client().Head(server.URL + test.path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if err != nil || closeErr != nil || len(body) != 0 || response.StatusCode != test.status || response.Header.Get("Allow") != test.allow {
			t.Fatalf("HEAD %s: status=%d body=%q allow=%q read=%v close=%v", test.path, response.StatusCode, body, response.Header.Get("Allow"), err, closeErr)
		}
		if test.status == http.StatusOK && response.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("HEAD snapshot Content-Type=%q", response.Header.Get("Content-Type"))
		}
	}
}

func TestHTTPWaitFailuresReturnServiceUnavailable(t *testing.T) {
	cfg := testConfig(t)
	for _, path := range []string{snapshotPath, commandsPath} {
		for _, stage := range []string{"send", "reply"} {
			for _, cause := range []string{"cancel", "deadline", "event loop exit"} {
				t.Run(path+"/"+stage+"/"+cause, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
						defer cancel()
						queue := make(chan runtimeRequest)
						loopDone := make(chan struct{})
						mux := newServeMux(runtimeRequests{queue: queue, done: loopDone}, newPrometheusMetrics(), cfg, zap.NewNop())
						method, body := http.MethodGet, ""
						if path == commandsPath {
							method, body = http.MethodPost, `{"action":"set-reader-workers","value":1}`
						}
						request := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
						recorder := httptest.NewRecorder()
						returned := make(chan struct{})
						go func() { mux.ServeHTTP(recorder, request); close(returned) }()
						if stage == "reply" {
							<-queue
						}
						synctest.Wait()
						switch cause {
						case "cancel":
							cancel()
						case "deadline":
							time.Sleep(time.Millisecond)
						case "event loop exit":
							close(loopDone)
						}
						<-returned
						if recorder.Code != http.StatusServiceUnavailable || recorder.Body.String() != "runtime unavailable\n" {
							t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
						}
					})
				})
			}
		}
	}
}

func TestHTTPCommandReceiptsHaveEmptyBody(t *testing.T) {
	for _, applied := range []bool{true, false} {
		requests := make(chan runtimeRequest)
		mux := newServeMux(runtimeRequests{queue: requests}, newPrometheusMetrics(), testConfig(t), zap.NewNop())
		go func() { (<-requests).(controlRequest).reply <- applied }()
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, commandsPath, strings.NewReader(`{"action":"set-reader-workers","value":1}`)))
		want := http.StatusOK
		if !applied {
			want = http.StatusConflict
		}
		if recorder.Code != want || recorder.Body.Len() != 0 {
			t.Fatalf("applied=%t status=%d body=%q", applied, recorder.Code, recorder.Body.String())
		}
	}
}

func TestStartHTTPServerLogsActualListenerAddress(t *testing.T) {
	cfg := testConfig(t)
	// The loader still rejects port zero; directly start the listener to distinguish actual from configured address.
	cfg.HTTP.Addr = "127.0.0.1:0"
	core, logs := observer.New(zap.InfoLevel)
	server, done, err := startHTTPServer(runtimeRequests{}, newPrometheusMetrics(), cfg, zap.New(core))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := shutdownHTTPServer(ctx, server); err != nil {
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
	if !ok || addr == cfg.HTTP.Addr || fields["event"] != "service_started" {
		t.Fatalf("start fields=%v", fields)
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		t.Fatal(err)
	}
}

func TestServeMuxRoutes(t *testing.T) {
	loaded := testConfig(t)
	loaded.Sender.API.URL = "https://example.test" + snapshotPath
	mux := newServeMux(runtimeRequests{queue: make(chan runtimeRequest, 1)}, newPrometheusMetrics(), loaded, zap.NewNop())

	for _, route := range []struct{ method, path, pattern string }{
		{http.MethodGet, snapshotPath, "GET " + snapshotPath},
		{http.MethodHead, snapshotPath, "GET " + snapshotPath},
		{http.MethodPost, commandsPath, "POST " + commandsPath},
	} {
		_, pattern := mux.Handler(httptest.NewRequest(route.method, route.path, nil))
		if pattern != route.pattern {
			t.Errorf("pattern=%q,want %q", pattern, route.pattern)
		}
	}
}

func TestServeMuxHasNoLocalIngest(t *testing.T) {
	mux := newServeMux(runtimeRequests{queue: make(chan runtimeRequest)}, newPrometheusMetrics(), testConfig(t), zap.NewNop())
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
	server, done, err := startHTTPServer(runtimeRequests{queue: make(chan runtimeRequest)}, newPrometheusMetrics(), loaded, zap.NewNop())
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
	server, done, err := startHTTPServer(runtimeRequests{queue: make(chan runtimeRequest)}, newPrometheusMetrics(), loaded, zap.NewNop())
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
