package ingest

import (
	"math"
	"net/http"
	"testing"
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
			server = newTestHandler(t, 1024, Config{ResponseStatus: test.input})
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

func TestConfigRejectsNonExactIngestPaths(t *testing.T) {
	for _, path := range []string{
		"ingest", "/", "/status", "/%73tatus", "//status", "/v1/", "/v1//batch",
		"/v1/../batch", "/v1/%2e/batch", "/v1/%2E%2E/batch", "/{rest...}", "/%7Brest...%7D",
		"/v1/batch?x=1", "/v1/batch#fragment", "/v1/a%20b", "/v1/a b", "/v1/%ZZ", "/v1/%00", "/v1/\u2003",
	} {
		t.Run(path, func(t *testing.T) {
			config := Config{Path: path, MaxBodyBytes: 1024}
			if err := config.Validate(); err == nil {
				t.Fatal("invalid path accepted by Validate")
			}
			if _, err := NewHandler(config); err == nil {
				t.Fatal("invalid programmatic path accepted")
			}
		})
	}
}

func TestConfigBodyLimitAndDelay(t *testing.T) {
	for _, test := range []struct {
		name         string
		maxBodyBytes int64
		delay        time.Duration
		wantErr      bool
	}{
		{name: "zero limit", wantErr: true},
		{name: "negative limit", maxBodyBytes: -1, wantErr: true},
		{name: "unbounded limit", maxBodyBytes: math.MaxInt64, wantErr: true},
		{name: "negative delay", maxBodyBytes: 1024, delay: -1, wantErr: true},
		{name: "zero delay", maxBodyBytes: 1},
		{name: "maximum values", maxBodyBytes: math.MaxInt64 - 1, delay: time.Duration(math.MaxInt64)},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := Config{Path: "/internal/ingest", MaxBodyBytes: test.maxBodyBytes, ResponseDelay: test.delay}
			if err := config.Validate(); (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %t", err, test.wantErr)
			}
			if _, err := NewHandler(config); (err != nil) != test.wantErr {
				t.Fatalf("NewHandler() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}
