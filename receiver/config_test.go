package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"receiver/internal/ingest"
)

func TestLoadConfigStrictRequiredTypes(t *testing.T) {
	for _, test := range []struct{ name, old, replacement string }{
		{"string delay", "response_delay_ms = 25", `response_delay_ms = "25"`},
		{"bool delay", "response_delay_ms = 25", "response_delay_ms = true"},
		{"string timeout", "shutdown_timeout_ms = 2000", `shutdown_timeout_ms = "2000"`},
		{"bool body limit", "max_body_bytes = 33554432", "max_body_bytes = true"},
		{"fractional body limit", "max_body_bytes = 33554432", "max_body_bytes = 33554432.9"},
		{"string status pointer", "response_status = 204", `response_status = "204"`},
		{"bool status pointer", "response_status = 204", "response_status = true"},
		{"fractional status pointer", "response_status = 204", "response_status = 204.9"},
		{"missing logging", "level = \"info\"\n", ""},
		{"missing schema", "schema_version = 2\n", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			content := strings.Replace(validTOMLConfig(), test.old, test.replacement, 1)
			if content == validTOMLConfig() {
				t.Fatal("replacement did not apply")
			}
			if _, err := LoadConfig(writeConfig(t, content)); err == nil || !strings.Contains(err.Error(), "decode config") {
				t.Fatalf("expected strict decode error, got %v", err)
			}
		})
	}
}

func TestLoadConfigDurationBoundsAndSchema(t *testing.T) {
	maximum := int64(math.MaxInt64 / time.Millisecond)
	for _, field := range []struct {
		line    string
		minimum int64
	}{
		{line: "shutdown_timeout_ms = 2000", minimum: 1},
		{line: "response_delay_ms = 25", minimum: 0},
	} {
		for _, value := range []int64{-1, field.minimum, maximum, maximum + 1} {
			t.Run(fmt.Sprintf("%s/%d", field.line, value), func(t *testing.T) {
				key := strings.Split(field.line, " = ")[0]
				content := strings.Replace(validTOMLConfig(), field.line, fmt.Sprintf("%s = %d", key, value), 1)
				_, err := LoadConfig(writeConfig(t, content))
				if (err != nil) != (value < field.minimum || value > maximum) {
					t.Fatalf("duration error=%v value=%d", err, value)
				}
			})
		}
	}
	for _, version := range []int{0, 1, 3} {
		content := strings.Replace(validTOMLConfig(), "schema_version = 2", fmt.Sprintf("schema_version = %d", version), 1)
		if _, err := LoadConfig(writeConfig(t, content)); err == nil || !strings.Contains(err.Error(), "schema_version") {
			t.Fatalf("unsupported schema %d accepted: %v", version, err)
		}
	}
}

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
			content := strings.Replace(validTOMLConfig(), `path = "/internal/ingest"`, fmt.Sprintf("path = %q", path), 1)
			if _, err := LoadConfig(writeConfig(t, content)); err == nil {
				t.Fatal("invalid configured path accepted")
			}
		})
	}
}

func TestLoadConfigMapsExactPaths(t *testing.T) {
	for _, path := range []string{"/internal/ingest", "/v1/batches", "/AZaz09._~-/.hidden/...", "/status/batches"} {
		t.Run(path, func(t *testing.T) {
			content := strings.Replace(validTOMLConfig(), `path = "/internal/ingest"`, fmt.Sprintf("path = %q", path), 1)
			config, err := LoadConfig(writeConfig(t, content))
			if err != nil {
				t.Fatal(err)
			}
			if config.Ingest.Path != path {
				t.Fatalf("path = %q, want %q", config.Ingest.Path, path)
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr bool
		check   func(t *testing.T, config Config)
	}{
		{
			name:    "accepts complete strict configuration",
			content: validTOMLConfig(),
			check: func(t *testing.T, config Config) {
				t.Helper()
				if config.SchemaVersion != 2 {
					t.Fatalf("SchemaVersion = %d, want 2", config.SchemaVersion)
				}
				if config.Server.Address != "127.0.0.1:18080" {
					t.Fatalf("Server.Address = %q, want loopback address", config.Server.Address)
				}
				if config.Server.ShutdownTimeout != 2*time.Second {
					t.Fatalf("Server.ShutdownTimeout = %s, want 2s", config.Server.ShutdownTimeout)
				}
				if config.Ingest.Path != "/internal/ingest" {
					t.Fatalf("Ingest.Path = %q, want /internal/ingest", config.Ingest.Path)
				}
				if config.Ingest.MaxBodyBytes != 32*1024*1024 {
					t.Fatalf("Ingest.MaxBodyBytes = %d, want 33554432", config.Ingest.MaxBodyBytes)
				}
				if config.Ingest.ResponseDelay != 25*time.Millisecond {
					t.Fatalf("Ingest.ResponseDelay = %s, want 25ms", config.Ingest.ResponseDelay)
				}
				if config.Ingest.ResponseStatus == nil || *config.Ingest.ResponseStatus != 204 {
					t.Fatalf("Ingest.ResponseStatus = %v, want pointer to 204", config.Ingest.ResponseStatus)
				}
			},
		},
		{
			name:    "rejects unknown field",
			content: validTOMLConfig() + "\n[unexpected]\nenabled = true\n",
			wantErr: true,
		},
		{
			name: "rejects unknown nested field",
			content: strings.Replace(
				validTOMLConfig(),
				"shutdown_timeout_ms = 2000",
				"shutdown_timeout_ms = 2000\nunexpected = true",
				1,
			),
			wantErr: true,
		},
		{
			name:    "rejects unsupported schema version",
			content: strings.Replace(validTOMLConfig(), "schema_version = 2", "schema_version = 1", 1),
			wantErr: true,
		},
		{
			name:    "rejects non internal ingest path",
			content: strings.Replace(validTOMLConfig(), "path = \"/internal/ingest\"", "path = \"ingest\"", 1),
			wantErr: true,
		},
		{
			name:    "rejects non positive body limit",
			content: strings.Replace(validTOMLConfig(), "max_body_bytes = 33554432", "max_body_bytes = 0", 1),
			wantErr: true,
		},
		{
			name:    "rejects non positive shutdown timeout",
			content: strings.Replace(validTOMLConfig(), "shutdown_timeout_ms = 2000", "shutdown_timeout_ms = 0", 1),
			wantErr: true,
		},
		{
			name:    "rejects unsupported logging level",
			content: strings.Replace(validTOMLConfig(), "level = \"info\"", "level = \"trace\"", 1),
			wantErr: true,
		},
		{
			name:    "rejects negative lab delay",
			content: strings.Replace(validTOMLConfig(), "response_delay_ms = 25", "response_delay_ms = -1", 1),
			wantErr: true,
		},
		{
			name:    "rejects lab status outside allowlist",
			content: strings.Replace(validTOMLConfig(), "response_status = 204", "response_status = 201", 1),
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeConfig(t, test.content)

			config, err := LoadConfig(path)
			if test.wantErr {
				if err == nil {
					t.Fatal("LoadConfig() error = nil, want validation error")
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			test.check(t, config)
		})
	}
}

func TestLoadConfigRequiresLabDelayAndRejectsExplicitZeroStatus(t *testing.T) {
	for _, test := range []struct{ name, old, replacement string }{
		{"missing delay", "response_delay_ms = 25\n", ""},
		{"explicit zero status", "response_status = 204", "response_status = 0"},
		{"fractional delay", "response_delay_ms = 25", "response_delay_ms = 1.9"},
	} {
		t.Run(test.name, func(t *testing.T) {
			content := strings.Replace(validTOMLConfig(), test.old, test.replacement, 1)
			if _, err := LoadConfig(writeConfig(t, content)); err == nil {
				t.Fatal("invalid lab setting accepted")
			}
		})
	}
}

func validTOMLConfig() string {
	return `schema_version = 2

[server]
address = "127.0.0.1:18080"
shutdown_timeout_ms = 2000

[ingest]
path = "/internal/ingest"
max_body_bytes = 33554432

[logging]
level = "info"

[lab]
response_delay_ms = 25
response_status = 204
`
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "sink.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}

func TestConfigResolvesOptionalResponseStatus(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		input   *int
		want    int
		wantErr bool
	}{
		{name: "absent", want: http.StatusNoContent},
		{name: "explicit zero", line: "response_status = 0", input: new(0), wantErr: true},
		{name: "success", line: "response_status = 204", input: new(204), want: http.StatusNoContent},
		{name: "bad request", line: "response_status = 400", input: new(400), want: http.StatusBadRequest},
		{name: "server error", line: "response_status = 500", input: new(500), want: http.StatusInternalServerError},
		{name: "unsupported", line: "response_status = 201", input: new(201), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := strings.Replace(validTOMLConfig(), "response_status = 204", test.line, 1)
			config, err := LoadConfig(writeConfig(t, content))
			if test.wantErr {
				if err == nil {
					t.Fatal("unsupported toml response status accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (config.Ingest.ResponseStatus == nil) != (test.input == nil) {
				t.Fatal("toml input presence changed")
			}
			if config.Ingest.ResponseStatus != nil && *config.Ingest.ResponseStatus != test.want {
				t.Fatalf("response status = %d, want %d", *config.Ingest.ResponseStatus, test.want)
			}
			handler, err := ingest.NewHandler(config.Ingest)
			if err != nil {
				t.Fatal(err)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/status", nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status endpoint code = %d, want 200", recorder.Code)
			}
			var status struct {
				Lab struct {
					ResponseDelayMS int `json:"responseDelayMs"`
					ResponseStatus  int `json:"responseStatus"`
				} `json:"lab"`
			}
			if err := json.NewDecoder(recorder.Body).Decode(&status); err != nil {
				t.Fatal(err)
			}
			if status.Lab.ResponseDelayMS != 25 || status.Lab.ResponseStatus != test.want {
				t.Fatalf("lab status = %+v, want delay 25 and status %d", status.Lab, test.want)
			}
		})
	}
}
