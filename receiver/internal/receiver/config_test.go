package receiver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
				if config.SchemaVersion != 1 {
					t.Fatalf("SchemaVersion = %d, want 1", config.SchemaVersion)
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
				if config.Lab.ResponseDelay != 25*time.Millisecond {
					t.Fatalf("Lab.ResponseDelay = %s, want 25ms", config.Lab.ResponseDelay)
				}
				if config.Lab.ResponseStatus == nil || *config.Lab.ResponseStatus != 204 {
					t.Fatalf("Lab.ResponseStatus = %v, want pointer to 204", config.Lab.ResponseStatus)
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
			content: strings.Replace(validTOMLConfig(), "schema_version = 1", "schema_version = 2", 1),
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

func validTOMLConfig() string {
	return `schema_version = 1

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
