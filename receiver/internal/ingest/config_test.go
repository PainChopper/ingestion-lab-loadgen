package ingest

import (
	"math"
	"testing"
	"time"
)

func TestConfigRejectsNonExactIngestPaths(t *testing.T) {
	for _, path := range []string{
		"/v1/a%2Fb", "/v1/%252F", "/v1/percent%25", "/v1/данные", "/v1/./batch",
		"/v1/a!b", "/v1/a$b", "/v1/a&b", "/v1/a'b", "/v1/a(b", "/v1/a)b", "/v1/a*b",
		"/v1/a+b", "/v1/a,b", "/v1/a;b", "/v1/a=b", "/v1/a:b", "/v1/a@b", "/v1/a\\b",
		"ingest", "/", "/status", "/%73tatus", "//status", "/v1/", "/v1//batch",
		"/v1/../batch", "/v1/%2e/batch", "/v1/%2E%2E/batch", "/{rest...}", "/%7Brest...%7D",
		"/v1/batch?x=1", "/v1/batch#fragment", "/v1/a%20b", "/v1/a b", "/v1/%ZZ", "/v1/%00", "/v1/\u2003",
	} {
		t.Run(path, func(t *testing.T) {
			config := handlerConfig()
			config.Path = path
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
			config := handlerConfig()
			config.MaxBodyBytes = test.maxBodyBytes
			config.ResponseDelay = test.delay
			if err := config.Validate(); (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %t", err, test.wantErr)
			}
			if _, err := NewHandler(config); (err != nil) != test.wantErr {
				t.Fatalf("NewHandler() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}

func handlerConfig() Config {
	return Config{Path: "/internal/ingest", MaxBodyBytes: 1024}
}
