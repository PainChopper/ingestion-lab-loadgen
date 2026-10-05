package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestServeMuxRoutes(t *testing.T) {
	routes := []string{
		snapshotPath,
		commandsPath,
		"/test/receive",
	}

	for _, route := range routes {
		requests := make(chan runtimeCommand, 1)
		loaded := testConfig(t)
		loaded.Sender.API.URL = "http://127.0.0.1:8080/test/receive"
		mux, err := newServeMux(requests, nil, loaded, nil)
		if err != nil {
			t.Fatal(err)
		}

		req := httptest.NewRequest(http.MethodGet, route, nil)
		_, pattern := mux.Handler(req)

		if pattern != route {
			t.Errorf("pattern = %q, want %q", pattern, route)
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
	server, done, err := startHTTPServer(make(chan runtimeCommand), nil, loaded, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		shutdownHTTPServer(ctx, server, done, nil)
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
	parsed, err := url.Parse(loaded.Sender.API.URL)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get("http://" + addr + parsed.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("configured server GET = %d, want %d", response.StatusCode, http.StatusMethodNotAllowed)
	}
	t.Logf("configured server %s answered GET %s with %d", addr, parsed.Path, response.StatusCode)
}

func TestStartHTTPServerReturnsOccupiedAddressError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	loaded := testConfig(t)
	loaded.HTTP.Addr = listener.Addr().String()
	server, done, err := startHTTPServer(make(chan runtimeCommand), nil, loaded, nil)
	if server != nil || done != nil {
		t.Fatal("failed startup returned server or done channel")
	}
	var listenError *net.OpError
	if !errors.As(err, &listenError) || listenError.Op != "listen" {
		t.Fatalf("startup error = %v, want wrapped listen error", err)
	}
	t.Logf("occupied address %s returned %v", loaded.HTTP.Addr, err)
}

func TestServeMuxUsesConfiguredIngestPath(t *testing.T) {
	tests := []struct {
		name string
		url  string
		path string
	}{
		{name: "new path", url: "http://example.test/test/receive", path: "/test/receive"},
		{name: "query", url: "http://example.test/test/receive?source=loadgen", path: "/test/receive"},
		{name: "root", url: "http://example.test", path: "/"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			loaded := testConfig(t)
			loaded.Sender.API.URL = test.url
			mux, err := newServeMux(make(chan runtimeCommand), nil, loaded, nil)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(mux)
			defer server.Close()
			response, err := server.Client().Post(server.URL+test.path, "application/json", strings.NewReader(`[{"client_id":"client-1"}]`))
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				t.Fatalf("POST %s = %d, want 204", test.path, response.StatusCode)
			}
			if test.path != "/" {
				response, err = server.Client().Post(server.URL+"/internal/test/ingest", "application/json", strings.NewReader(`[{"client_id":"client-1"}]`))
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				if response.StatusCode != http.StatusNotFound {
					t.Fatalf("old path POST = %d, want 404", response.StatusCode)
				}
			}
			t.Logf("configured URL %s: real POST %s returned 204", test.url, test.path)
		})
	}
}

func TestStartHTTPServerRejectsInvalidIngestRouteBeforeListen(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "malformed URL", url: "http://example.test/%invalid"},
		{name: "snapshot", url: "http://example.test" + snapshotPath},
		{name: "commands", url: "http://example.test" + commandsPath},
		{name: "metrics", url: "http://example.test/metrics"},
		{name: "spec", url: "http://example.test" + developerOpenAPIPath},
		{name: "swagger prefix", url: "http://example.test" + developerSwaggerUIPath},
		{name: "swagger asset", url: "http://example.test" + developerSwaggerUIPath + "swagger-ui-bundle.js"},
		{name: "swagger redirect", url: "http://example.test" + strings.TrimSuffix(developerSwaggerUIPath, "/")},
		{name: "pprof prefix", url: "http://example.test/debug/pprof/"},
		{name: "pprof subtree", url: "http://example.test/debug/pprof/goroutine"},
		{name: "pprof redirect", url: "http://example.test/debug/pprof"},
		{name: "wildcard syntax", url: "http://example.test/{path}"},
		{name: "pattern whitespace", url: "http://example.test/a%20b"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			loaded := testConfig(t)
			loaded.HTTP.Addr = "invalid-listen-address"
			loaded.Sender.API.URL = test.url
			server, done, err := startHTTPServer(make(chan runtimeCommand), NewPrometheusMetrics(), loaded, nil)
			if server != nil || done != nil || err == nil || !strings.Contains(err.Error(), "sender.api.url") {
				t.Fatalf("startup = %v, %v, %v; want named sender.api.url error before listen", server, done, err)
			}
			var listenError *net.OpError
			if errors.As(err, &listenError) {
				t.Fatalf("invalid route reached listen: %v", err)
			}
			t.Logf("%s returned %v before listen", test.url, err)
		})
	}
}
