package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServeMuxRoutes(t *testing.T) {
	routes := []string{
		snapshotPath,
		commandsPath,
		internalTestIngestPath,
	}

	for _, route := range routes {
		requests := make(chan runtimeCommand, 1)
		mux := newServeMux(requests, nil, testConfig(t), nil)

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
	response, err := client.Get("http://" + addr + internalTestIngestPath)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("configured server GET = %d, want %d", response.StatusCode, http.StatusMethodNotAllowed)
	}
	t.Logf("configured server %s answered GET %s with %d", addr, internalTestIngestPath, response.StatusCode)
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
