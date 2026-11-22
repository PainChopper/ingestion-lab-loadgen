package config_test

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PainChopper/ingestion-lab-loadgen/receiver/internal/config"
	"github.com/go-viper/mapstructure/v2"
)

const validConfigPath = "testdata/valid.toml"

func TestLoadConfigRejectsUnknownNames(t *testing.T) {
	base := readValidTOML(t)
	for _, test := range []struct{ name, contents, unknown string }{
		{"empty section", base + "\n[unexpected]\n", "unexpected"},
		{"unknown section", base + "\n[unexpected]\nenabled = true\n", "unexpected"},
		{"unknown nested field", replaceTOML(t, base, "[server]", "[server]\nunexpected = true"), "unexpected"},
		{"wrong-case section", replaceTOML(t, base, "[server]", "[Server]"), "Server"},
		{"wrong-case key", replaceTOML(t, base, "address =", "Address ="), "Address"},
		{"additional wrong-case section", base + "\n[Server]\naddress = \"127.0.0.1:18080\"\nshutdown_timeout_ms = 2000\n", "Server"},
		{"additional wrong-case key", strings.Replace(base, "[server]", "[server]\nAddress = \"127.0.0.1:18080\"", 1), "Address"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := config.Load(writeConfig(t, test.contents))
			var decodeErr *mapstructure.DecodeError
			// For case mismatches both unknown and missing fields may be reported.
			if !errors.As(err, &decodeErr) || !strings.Contains(err.Error(), "invalid keys: "+test.unknown) {
				t.Fatalf("error = %v, want unknown key %q", err, test.unknown)
			}
		})
	}
}

func TestLoadConfigStrictRequiredTypes(t *testing.T) {
	base := readValidTOML(t)
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
		{"missing address", "address = \"127.0.0.1:18080\"\n", ""},
		{"missing timeout", "shutdown_timeout_ms = 2000\n", ""},
		{"missing path", "path = \"/internal/ingest\"\n", ""},
		{"missing body limit", "max_body_bytes = 33554432\n", ""},
		{"missing delay", "response_delay_ms = 25\n", ""},
		{"fractional delay", "response_delay_ms = 25", "response_delay_ms = 1.9"},
	} {
		t.Run(test.name, func(t *testing.T) {
			content := replaceTOML(t, base, test.old, test.replacement)
			_, err := config.Load(writeConfig(t, content))
			var decodeErr *mapstructure.DecodeError
			if !errors.As(err, &decodeErr) {
				t.Fatalf("expected strict decode error, got %v", err)
			}
		})
	}
}

func TestLoadConfigDurationBounds(t *testing.T) {
	base := readValidTOML(t)
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
				content := replaceTOML(t, base, field.line, fmt.Sprintf("%s = %d", key, value))
				_, err := config.Load(writeConfig(t, content))
				if (err != nil) != (value < field.minimum || value > maximum) {
					t.Fatalf("duration error=%v value=%d", err, value)
				}
			})
		}
	}
}

func TestLoadConfigMapsExactPath(t *testing.T) {
	const path = "/AZaz09._~-/.hidden/..."
	content := replaceTOML(t, readValidTOML(t), `path = "/internal/ingest"`, fmt.Sprintf("path = %q", path))
	config, err := config.Load(writeConfig(t, content))
	if err != nil {
		t.Fatal(err)
	}
	if config.Ingest.Path != path {
		t.Fatalf("path=%q, want %q", config.Ingest.Path, path)
	}
}

func TestLoadConfigMapsTOML(t *testing.T) {
	cfg, err := config.Load(validConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Logging.Level != "info" {
		t.Fatalf("logging level = %q, want info", cfg.Logging.Level)
	}
	if cfg.Server.Address != "127.0.0.1:18080" {
		t.Fatalf("Server.Address = %q, want loopback address", cfg.Server.Address)
	}
	if cfg.Server.ShutdownTimeout != 2*time.Second {
		t.Fatalf("Server.ShutdownTimeout = %s, want 2s", cfg.Server.ShutdownTimeout)
	}
	if cfg.Ingest.Path != "/internal/ingest" {
		t.Fatalf("Ingest.Path = %q, want /internal/ingest", cfg.Ingest.Path)
	}
	if cfg.Ingest.MaxBodyBytes != 32*1024*1024 {
		t.Fatalf("Ingest.MaxBodyBytes = %d, want 33554432", cfg.Ingest.MaxBodyBytes)
	}
	if cfg.Ingest.ResponseDelay != 25*time.Millisecond {
		t.Fatalf("Ingest.ResponseDelay = %s, want 25ms", cfg.Ingest.ResponseDelay)
	}
	if cfg.Ingest.ResponseStatus == nil || *cfg.Ingest.ResponseStatus != http.StatusNoContent {
		t.Fatalf("Ingest.ResponseStatus = %v, want pointer to 204", cfg.Ingest.ResponseStatus)
	}
}

func TestLoadConfigRejectsInvalidValues(t *testing.T) {
	base := readValidTOML(t)
	for _, test := range []struct {
		name        string
		old         string
		replacement string
		wantField   string
	}{
		{name: "empty address", old: `address = "127.0.0.1:18080"`, replacement: `address = ""`},
		{name: "nonpositive body limit", old: "max_body_bytes = 33554432", replacement: "max_body_bytes = 0"},
		{name: "nonpositive shutdown timeout", old: "shutdown_timeout_ms = 2000", replacement: "shutdown_timeout_ms = 0"},
		{name: "unsupported logging level", old: `level = "info"`, replacement: `level = "trace"`},
		{name: "invalid ingest path", old: `path = "/internal/ingest"`, replacement: `path = "/v1/a!b"`, wantField: "ingest.path"},
	} {
		t.Run(test.name, func(t *testing.T) {
			content := replaceTOML(t, base, test.old, test.replacement)
			_, err := config.Load(writeConfig(t, content))
			if err == nil {
				t.Fatal("invalid value accepted")
			}
			var decodeErr *mapstructure.DecodeError
			if errors.As(err, &decodeErr) {
				t.Fatalf("got decode error %v, want validation error", err)
			}
			if test.wantField != "" && !strings.Contains(err.Error(), test.wantField) {
				t.Fatalf("error = %v, want it to identify %s", err, test.wantField)
			}
		})
	}
}

func readValidTOML(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile(validConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func replaceTOML(t *testing.T, content, old, replacement string) string {
	t.Helper()
	if !strings.Contains(content, old) {
		t.Fatalf("fixture does not contain %q", old)
	}
	return strings.Replace(content, old, replacement, 1)
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "receiver.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}

func TestLoadConfigDecodesOptionalResponseStatus(t *testing.T) {
	base := readValidTOML(t)
	tests := []struct {
		name        string
		line        string
		want        int
		wantOmitted bool
		wantErr     bool
	}{
		{name: "absent", wantOmitted: true},
		{name: "explicit zero", line: "response_status = 0", wantErr: true},
		{name: "bad request", line: "response_status = 400", want: http.StatusBadRequest},
		{name: "server error", line: "response_status = 500", want: http.StatusInternalServerError},
		{name: "unsupported", line: "response_status = 201", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := replaceTOML(t, base, "response_status = 204", test.line)
			config, err := config.Load(writeConfig(t, content))
			if test.wantErr {
				if err == nil {
					t.Fatal("unsupported toml response status accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (config.Ingest.ResponseStatus == nil) != test.wantOmitted {
				t.Fatal("toml input presence changed")
			}
			if config.Ingest.ResponseStatus != nil && *config.Ingest.ResponseStatus != test.want {
				t.Fatalf("response status = %d, want %d", *config.Ingest.ResponseStatus, test.want)
			}
		})
	}
}
