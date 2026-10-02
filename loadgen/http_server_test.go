package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
