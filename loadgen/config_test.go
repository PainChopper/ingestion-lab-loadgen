package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

const testSenderURL = "http://127.0.0.1:8081/internal/ingest"

func testConfig(t *testing.T) config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(testConfigContents()), 0o600); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	loaded, err := loadConfig(path)
	if err != nil {
		t.Fatalf("load test config: %v", err)
	}
	return loaded
}

func testConfigContents() string {
	return strings.Join([]string{
		"schema_version = 3", "", "[source]", "path = 'C:\\dataset\\*.parquet'", "unit = \"glob-pattern\"", "mutability = \"startup-only\"", "",
		"[http]", "addr = \"127.0.0.1:8080\"", "",
		"[reader.read_batch_size]", "initial = 1000", "min = 1000", "max = 100000", "step = 1000", "unit = \"transactions\"", "mutability = \"startup-only\"", "",
		"[reader.workers]", "initial = 1", "min = 1", "max = 7", "step = 1", "unit = \"workers\"", "mutability = \"immediate\"", "",
		"[readerChannel.capacity]", "initial = 2", "allowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]", "unit = \"batches\"", "mutability = \"startup-only\"",
		"", "[senderChannel.capacity]", "initial = 0", "allowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]", "unit = \"batches\"", "mutability = \"startup-only\"",
		"", "[throttler.requested_tps]", "initial = 2000000", "min = 0", "max = 4000000", "step = 200000", "unit = \"transactions/s\"", "mutability = \"immediate\"",
		"", "[throttler.installed]", "initial = true", "allowed = [true, false]", "mutability = \"immediate\"",
		"", "[sender.workers]", "initial = 32", "min = 1", "max = 32", "step = 1", "unit = \"workers\"", "mutability = \"immediate\"",
		"", "[sender.api]", "timeout_ms = 30000", "url = \"" + testSenderURL + "\"", "mutability = \"startup-only\"",
		"", "[sender.retry]", "delays_ms = [250, 500, 1000, 2000, 5000]", "jitter_percent = 20", "mutability = \"startup-only\"",
		"", "[metrics.window_ms]", "initial = 1000", "min = 100", "max = 10000", "step = 100", "unit = \"milliseconds\"", "mutability = \"startup-only\"",
		"", "[logging]", "level = \"info\"", "mutability = \"startup-only\"",
	}, "\n")
}

func TestLoadConfigInitialContract(t *testing.T) {
	contents := testConfigContents()
	fields := []struct{ section, value string }{
		{section: "reader.read_batch_size", value: "1000"}, {section: "reader.workers", value: "1"},
		{section: "readerChannel.capacity", value: "2"}, {section: "senderChannel.capacity", value: "0"},
		{section: "throttler.requested_tps", value: "2000000"},
		{section: "throttler.installed", value: "true"},
		{section: "sender.workers", value: "32"}, {section: "metrics.window_ms", value: "1000"},
	}
	for _, field := range fields {
		for _, shape := range []string{"initial-only", "legacy-only", "both", "missing"} {
			t.Run(field.section+"/"+shape, func(t *testing.T) {
				prefix := "[" + field.section + "]\n"
				line := "initial = " + field.value + "\n"
				replacement := line
				switch shape {
				case "legacy-only":
					replacement = "default = " + field.value + "\n"
				case "both":
					replacement += "default = " + field.value + "\n"
				case "missing":
					replacement = ""
				}
				if field.section == "throttler.installed" && shape == "initial-only" {
					replacement = "initial = false\n"
				}
				input := strings.Replace(contents, prefix+line, prefix+replacement, 1)
				if shape != "initial-only" && input == contents {
					t.Fatal("contract mutation did not apply")
				}
				path := filepath.Join(t.TempDir(), "config.toml")
				if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
					t.Fatal(err)
				}
				_, err := loadConfig(path)
				if shape == "initial-only" && err != nil {
					t.Fatalf("initial config rejected: %v", err)
				}
				if shape != "initial-only" && err == nil {
					t.Fatal("legacy or incomplete config accepted")
				}
			})
		}
	}
}

func TestLoadConfigRejectsWeakNumericTypes(t *testing.T) {
	for _, test := range []struct{ name, old, replacement string }{
		{"worker string", "[reader.workers]\ninitial = 1", "[reader.workers]\ninitial = \"1\""},
		{"worker bool", "[reader.workers]\ninitial = 1", "[reader.workers]\ninitial = true"},
		{"reader fraction", "[reader.workers]\ninitial = 1", "[reader.workers]\ninitial = 1.9"},
		{"reader NaN", "[reader.workers]\ninitial = 1", "[reader.workers]\ninitial = nan"},
		{"reader infinity", "[reader.workers]\ninitial = 1", "[reader.workers]\ninitial = inf"},
		{"reader float overflow", "[reader.workers]\ninitial = 1", "[reader.workers]\ninitial = 9223372036854775808.0"},
		{"sender fraction", "[sender.workers]\ninitial = 32", "[sender.workers]\ninitial = 32.9"},
		{"metrics bool", "[metrics.window_ms]\ninitial = 1000", "[metrics.window_ms]\ninitial = true"},
		{"jitter string", "jitter_percent = 20", "jitter_percent = \"20\""},
		{"jitter fraction", "jitter_percent = 20", "jitter_percent = 20.9"},
		{"retry string elements", "delays_ms = [250, 500, 1000, 2000, 5000]", "delays_ms = [\"250\", \"500\"]"},
		{"retry bool elements", "delays_ms = [250, 500, 1000, 2000, 5000]", "delays_ms = [true]"},
		{"retry fraction element", "delays_ms = [250, 500, 1000, 2000, 5000]", "delays_ms = [250.9]"},
		{"retry scalar string", "delays_ms = [250, 500, 1000, 2000, 5000]", "delays_ms = \"250,500\""},
	} {
		t.Run(test.name, func(t *testing.T) {
			contents := strings.Replace(testConfigContents(), test.old, test.replacement, 1)
			if contents == testConfigContents() {
				t.Fatal("replacement did not apply")
			}
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadConfig(path); err == nil || !strings.Contains(err.Error(), "decode config") {
				t.Fatalf("expected a decoding error, got %v", err)
			}
		})
	}
}

func TestLoadConfigValidatesSenderAPI(t *testing.T) {
	contents := testConfigContents()
	urlLine := "url = \"" + testSenderURL + "\"\n"
	tests := []struct {
		name     string
		contents string
		wantErr  bool
	}{
		{name: "default", contents: contents},
		{name: "external HTTPS", contents: strings.Replace(contents, testSenderURL, "https://ingest.example.test/v1/batches", 1)},
		{name: "missing URL", contents: strings.Replace(contents, urlLine, "", 1), wantErr: true},
		{name: "malformed URL", contents: strings.Replace(contents, testSenderURL, "http://example.test/%invalid", 1), wantErr: true},
		{name: "relative URL", contents: strings.Replace(contents, testSenderURL, "/internal/ingest", 1), wantErr: true},
		{name: "unsupported scheme", contents: strings.Replace(contents, testSenderURL, "ftp://ingest.example.test/batches", 1), wantErr: true},
		{name: "missing host", contents: strings.Replace(contents, testSenderURL, "http:/internal/ingest", 1), wantErr: true},
		{name: "fragment", contents: strings.Replace(contents, testSenderURL, "https://ingest.example.test/batches#fragment", 1), wantErr: true},
		{
			name: "runtime mutability",
			contents: strings.Replace(
				contents,
				urlLine+"mutability = \"startup-only\"",
				urlLine+"mutability = \"immediate\"",
				1,
			),
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(test.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			loaded, err := loadConfig(path)
			if test.wantErr {
				if err == nil {
					t.Fatal("loadConfig error = nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("loadConfig: %v", err)
			}
			if loaded.Sender.API.URL == "" {
				t.Fatal("loaded sender API URL is empty")
			}
		})
	}
}

func TestLoadConfigValidatesSenderAPITimeout(t *testing.T) {
	const maxMS = math.MaxInt64 / int64(time.Millisecond)
	contents := strings.Replace(testConfigContents(), "timeout_ms = 30000\n", "", 1)
	tests := []struct {
		name    string
		line    string
		version int
		wantErr string
	}{
		{name: "minimum inclusive", line: "timeout_ms = 1\n", version: 3},
		{name: "integral float", line: "timeout_ms = 1.0\n", version: 3},
		{name: "maximum inclusive", line: fmt.Sprintf("timeout_ms = %d\n", maxMS), version: 3},
		{name: "missing", version: 3, wantErr: "timeout_ms"},
		{name: "zero", line: "timeout_ms = 0\n", version: 3, wantErr: "timeout_ms"},
		{name: "negative", line: "timeout_ms = -1\n", version: 3, wantErr: "timeout_ms"},
		{name: "overflow", line: fmt.Sprintf("timeout_ms = %d\n", maxMS+1), version: 3, wantErr: "timeout_ms"},
		{name: "fraction", line: "timeout_ms = 1.9\n", version: 3, wantErr: "timeout_ms"},
		{name: "string", line: "timeout_ms = \"1\"\n", version: 3, wantErr: "timeout_ms"},
		{name: "legacy schema", line: "timeout_ms = 1\n", version: 2, wantErr: "schema_version"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := strings.Replace(contents, "schema_version = 3", fmt.Sprintf("schema_version = %d", test.version), 1)
			input = strings.Replace(input, "[sender.api]\n", "[sender.api]\n"+test.line, 1)
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := loadConfig(path)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("loadConfig: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("loadConfig error = %v, want error about %s", err, test.wantErr)
			}
		})
	}
}

func TestLoadConfigValidatesHTTPAddress(t *testing.T) {
	section := "[http]\naddr = \"127.0.0.1:8080\"\n"
	tests := []struct {
		name           string
		addr           string
		missingSection bool
		missingAddr    bool
		wantErr        bool
	}{
		{name: "example", addr: "127.0.0.1:8080"},
		{name: "hostname", addr: "localhost:8081"},
		{name: "IPv6", addr: "[::1]:8081"},
		{name: "minimum port no DNS", addr: "host.example.test:1"},
		{name: "maximum port", addr: "127.0.0.1:65535"},
		{name: "missing section", missingSection: true, wantErr: true},
		{name: "missing addr", missingAddr: true, wantErr: true},
		{name: "empty", wantErr: true},
		{name: "missing port", addr: "127.0.0.1", wantErr: true},
		{name: "empty host", addr: ":8081", wantErr: true},
		{name: "empty port", addr: "127.0.0.1:", wantErr: true},
		{name: "zero port", addr: "127.0.0.1:0", wantErr: true},
		{name: "port overflow", addr: "127.0.0.1:65536", wantErr: true},
		{name: "negative port", addr: "127.0.0.1:-1", wantErr: true},
		{name: "signed port", addr: "127.0.0.1:+1", wantErr: true},
		{name: "named port", addr: "127.0.0.1:http", wantErr: true},
		{name: "unbracketed IPv6", addr: "::1:8081", wantErr: true},
		{name: "URL", addr: "http://127.0.0.1:8081", wantErr: true},
		{name: "path", addr: "localhost/path:8081", wantErr: true},
		{name: "whitespace host", addr: " :8081", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			replacement := fmt.Sprintf("[http]\naddr = %q\n", test.addr)
			if test.missingSection {
				replacement = ""
			} else if test.missingAddr {
				replacement = "[http]\n"
			}
			contents := strings.Replace(testConfigContents(), section, replacement, 1)
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			loaded, err := loadConfig(path)
			if test.wantErr {
				if err == nil || !strings.Contains(err.Error(), "http.addr") {
					t.Fatalf("loadConfig error = %v, want http.addr error", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if loaded.HTTP.Addr != test.addr {
				t.Fatalf("HTTP addr = %q, want %q", loaded.HTTP.Addr, test.addr)
			}
		})
	}
}

func TestLoadConfigRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct{ name, contents string }{
		{name: "unknown top-level key", contents: testConfigContents() + "\nunknown = true\n"},
		{name: "incomplete", contents: "[source]\npath = 'C:\\dataset\\*.parquet'"},
		{name: "relative source", contents: strings.ReplaceAll(testConfigContents(), "C:\\dataset\\*.parquet", "data/*.parquet")},
		{name: "invalid range", contents: strings.ReplaceAll(testConfigContents(), "step = 1000", "step = 0")},
		{name: "invalid allowed list", contents: strings.ReplaceAll(testConfigContents(), "[0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]", "[0, 2, 1]")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(test.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadConfig(path); err == nil {
				t.Fatal("loadConfig error = nil")
			}
		})
	}
}

func TestLoadConfigRequiresExplicitConfigFields(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
	}{
		{name: "schema version", old: "schema_version = 3\n", new: ""},
		{name: "source path", old: "[source]\npath = 'C:\\dataset\\*.parquet'\n", new: "[source]\n"},
		{name: "source unit", old: "path = 'C:\\dataset\\*.parquet'\nunit = \"glob-pattern\"\n", new: "path = 'C:\\dataset\\*.parquet'\n"},
		{name: "source mutability", old: "unit = \"glob-pattern\"\nmutability = \"startup-only\"\n", new: "unit = \"glob-pattern\"\n"},
		{name: "reader batch initial", old: "[reader.read_batch_size]\ninitial = 1000\n", new: "[reader.read_batch_size]\n"},
		{name: "reader batch min", old: "initial = 1000\nmin = 1000\n", new: "initial = 1000\n"},
		{name: "reader batch max", old: "min = 1000\nmax = 100000\n", new: "min = 1000\n"},
		{name: "reader batch step", old: "max = 100000\nstep = 1000\n", new: "max = 100000\n"},
		{name: "reader batch unit", old: "step = 1000\nunit = \"transactions\"\n", new: "step = 1000\n"},
		{name: "reader batch mutability", old: "unit = \"transactions\"\nmutability = \"startup-only\"\n", new: "unit = \"transactions\"\n"},
		{name: "reader channel initial", old: "[readerChannel.capacity]\ninitial = 2\n", new: "[readerChannel.capacity]\n"},
		{name: "reader channel allowed", old: "initial = 2\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\n", new: "initial = 2\n"},
		{name: "reader channel unit", old: "allowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\nunit = \"batches\"\n", new: "allowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\n"},
		{name: "reader channel mutability", old: "[readerChannel.capacity]\ninitial = 2\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\nunit = \"batches\"\nmutability = \"startup-only\"\n", new: "[readerChannel.capacity]\ninitial = 2\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\nunit = \"batches\"\n"},
		{name: "sender channel initial", old: "[senderChannel.capacity]\ninitial = 0\n", new: "[senderChannel.capacity]\n"},
		{name: "sender channel allowed", old: "[senderChannel.capacity]\ninitial = 0\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\n", new: "[senderChannel.capacity]\ninitial = 0\n"},
		{name: "sender channel unit", old: "[senderChannel.capacity]\ninitial = 0\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\nunit = \"batches\"\n", new: "[senderChannel.capacity]\ninitial = 0\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\n"},
		{name: "sender channel mutability", old: "[senderChannel.capacity]\ninitial = 0\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\nunit = \"batches\"\nmutability = \"startup-only\"\n", new: "[senderChannel.capacity]\ninitial = 0\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\nunit = \"batches\"\n"},
		{name: "requested TPS initial", old: "[throttler.requested_tps]\ninitial = 2000000\n", new: "[throttler.requested_tps]\n"},
		{name: "requested TPS min", old: "initial = 2000000\nmin = 0\n", new: "initial = 2000000\n"},
		{name: "requested TPS max", old: "min = 0\nmax = 4000000\n", new: "min = 0\n"},
		{name: "requested TPS step", old: "max = 4000000\nstep = 200000\n", new: "max = 4000000\n"},
		{name: "requested TPS unit", old: "step = 200000\nunit = \"transactions/s\"\n", new: "step = 200000\n"},
		{name: "requested TPS mutability", old: "unit = \"transactions/s\"\nmutability = \"immediate\"\n", new: "unit = \"transactions/s\"\n"},
		{name: "installation mode initial", old: "[throttler.installed]\ninitial = true\n", new: "[throttler.installed]\n"},
		{name: "installation mode allowed", old: "initial = true\nallowed = [true, false]\n", new: "initial = true\n"},
		{name: "installation mode mutability", old: "allowed = [true, false]\nmutability = \"immediate\"", new: "allowed = [true, false]\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contents := strings.Replace(testConfigContents(), test.old, test.new, 1)
			if contents == testConfigContents() {
				t.Fatalf("test replacement %q did not apply", test.old)
			}
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadConfig(path); err == nil {
				t.Fatal("loadConfig accepted config with a missing configuration field")
			}
		})
	}
}

func TestLoadConfigAllowsExplicitZeroValues(t *testing.T) {
	tests := []struct {
		name        string
		contents    string
		mustContain string
	}{
		{
			name:        "reader channel capacity initial",
			contents:    strings.Replace(testConfigContents(), "[readerChannel.capacity]\ninitial = 2\n", "[readerChannel.capacity]\ninitial = 0\n", 1),
			mustContain: "[readerChannel.capacity]\ninitial = 0\n",
		},
		{
			name:        "sender channel capacity initial",
			contents:    testConfigContents(),
			mustContain: "[senderChannel.capacity]\ninitial = 0\n",
		},
		{
			name:        "requested TPS minimum",
			contents:    testConfigContents(),
			mustContain: "[throttler.requested_tps]\ninitial = 2000000\nmin = 0\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !strings.Contains(test.contents, test.mustContain) {
				t.Fatalf("explicit zero config does not contain %q", test.mustContain)
			}
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(test.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadConfig(path); err != nil {
				t.Fatalf("loadConfig explicit zero: %v", err)
			}
		})
	}
}

func TestLoadConfigAllowsSenderChannelCapacityConfig(t *testing.T) {
	contents := strings.Replace(
		testConfigContents(),
		"[senderChannel.capacity]\ninitial = 0\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]",
		"[senderChannel.capacity]\ninitial = 3\nallowed = [0, 3, 7]",
		1,
	)
	if contents == testConfigContents() {
		t.Fatal("sender channel capacity setting replacement did not apply")
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(path); err != nil {
		t.Fatalf("loadConfig sender channel capacity config: %v", err)
	}
}

func TestLoadConfigRejectsInvalidSenderChannelCapacityConfig(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
	}{
		{name: "wrong unit", old: "[senderChannel.capacity]\ninitial = 0\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\nunit = \"batches\"", new: "[senderChannel.capacity]\ninitial = 0\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\nunit = \"transactions\""},
		{name: "wrong mutability", old: "[senderChannel.capacity]\ninitial = 0\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\nunit = \"batches\"\nmutability = \"startup-only\"", new: "[senderChannel.capacity]\ninitial = 0\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\nunit = \"batches\"\nmutability = \"unsupported-mode\""},
		{name: "negative allowed value", old: "allowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]", new: "allowed = [-1, 0, 1]"},
		{name: "not strictly increasing allowed values", old: "allowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]", new: "allowed = [0, 2, 1]"},
		{name: "initial outside allowed", old: "[senderChannel.capacity]\ninitial = 0", new: "[senderChannel.capacity]\ninitial = 3"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contents := strings.Replace(testConfigContents(), test.old, test.new, 1)
			if contents == testConfigContents() {
				t.Fatalf("test replacement %q did not apply", test.old)
			}
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadConfig(path); err == nil {
				t.Fatal("loadConfig accepted invalid sender channel capacity config")
			}
		})
	}
}

func TestThrottlerConfigUsesApprovedProfile(t *testing.T) {
	loaded := testConfig(t)
	reader := loaded.Reader.ReadBatchSize
	if reader.Initial != 1_000 || reader.Min != 1_000 || reader.Max != 100_000 ||
		reader.Step != 1_000 || reader.Unit != batchSizeUnit || reader.Mutability != startupOnly {
		t.Fatalf("reader batch size config = %+v", reader)
	}
	requested := loaded.Throttler.RequestedTPS
	if requested.Initial != 2_000_000 || requested.Min != 0 || requested.Max != 4_000_000 ||
		requested.Step != 200_000 || requested.Unit != requestedTPSUnit || requested.Mutability != immediate {
		t.Fatalf("requested TPS config = %+v", requested)
	}
	values := make(map[int]struct{})
	for value := requested.Min; value <= requested.Max; value += requested.Step {
		values[value] = struct{}{}
	}
	if len(values) != 21 || !requested.contains(requested.Initial) {
		t.Fatalf("requested TPS grid = %d values, initial valid = %t", len(values), requested.contains(requested.Initial))
	}
	mode := loaded.Throttler.Installed
	if mode.Initial != true || !mode.contains(true) ||
		!mode.contains(false) || mode.Mutability != immediate {
		t.Fatalf("installation mode config = %+v", mode)
	}
}

func TestMetricsWindowConfigUsesApprovedProfile(t *testing.T) {
	window := testConfig(t).Metrics.WindowMS
	if window.Initial != 1_000 || window.Min != 100 || window.Max != 10_000 ||
		window.Step != 100 || window.Unit != metricsWindowUnit || window.Mutability != startupOnly {
		t.Fatalf("metrics window config = %+v", window)
	}
}

func TestLoadConfigRejectsInvalidMetricsWindowConfig(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
	}{
		{name: "missing initial", old: "[metrics.window_ms]\ninitial = 1000", new: "[metrics.window_ms]"},
		{name: "wrong unit", old: "unit = \"milliseconds\"", new: "unit = \"seconds\""},
		{name: "wrong mutability", old: "[metrics.window_ms]\ninitial = 1000\nmin = 100\nmax = 10000\nstep = 100\nunit = \"milliseconds\"\nmutability = \"startup-only\"", new: "[metrics.window_ms]\ninitial = 1000\nmin = 100\nmax = 10000\nstep = 100\nunit = \"milliseconds\"\nmutability = \"immediate\""},
		{name: "below minimum", old: "[metrics.window_ms]\ninitial = 1000\nmin = 100", new: "[metrics.window_ms]\ninitial = 1000\nmin = 99"},
		{name: "above maximum", old: "[metrics.window_ms]\ninitial = 1000\nmin = 100\nmax = 10000", new: "[metrics.window_ms]\ninitial = 1000\nmin = 100\nmax = 10100"},
		{name: "off grid initial", old: "[metrics.window_ms]\ninitial = 1000", new: "[metrics.window_ms]\ninitial = 1050"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contents := strings.Replace(testConfigContents(), test.old, test.new, 1)
			if contents == testConfigContents() {
				t.Fatalf("test replacement %q did not apply", test.old)
			}
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadConfig(path); err == nil {
				t.Fatal("loadConfig accepted invalid metrics window config")
			}
		})
	}
}

func TestLoadConfigAllowsSafeMetricsWindowInitials(t *testing.T) {
	for _, value := range []int{100, 300, 1_000, 10_000} {
		t.Run(fmt.Sprintf("%d milliseconds", value), func(t *testing.T) {
			contents := strings.Replace(
				testConfigContents(),
				"[metrics.window_ms]\ninitial = 1000",
				fmt.Sprintf("[metrics.window_ms]\ninitial = %d", value),
				1,
			)
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadConfig(path); err != nil {
				t.Fatalf("loadConfig metrics window %d: %v", value, err)
			}
		})
	}
}

func TestCheckedInConfigLoadsApprovedThrottlerProfile(t *testing.T) {
	loaded, err := loadConfig(defaultConfigPath)
	if err != nil {
		t.Fatalf("load checked-in config: %v", err)
	}
	if loaded.Reader.ReadBatchSize.Initial != 1_000 ||
		loaded.Throttler.RequestedTPS.Initial != 2_000_000 || loaded.Throttler.RequestedTPS.Min != 0 ||
		loaded.Throttler.RequestedTPS.Max != 4_000_000 || loaded.Throttler.RequestedTPS.Step != 200_000 ||
		loaded.Throttler.Installed.Initial != true {
		t.Fatalf("checked-in throttler config = %+v", loaded.Throttler)
	}
}

func TestLoadConfigRejectsInvalidThrottlerConfig(t *testing.T) {
	tests := []struct {
		name      string
		old       string
		new       string
		wantError string
	}{
		{name: "missing TPS initial", old: "[throttler.requested_tps]\ninitial = 2000000", new: "[throttler.requested_tps]"},
		{name: "missing mode allowed", old: "allowed = [true, false]", new: ""},
		{name: "unknown key", old: "[throttler.installed]", new: "[throttler.installed]\nextra = true"},
		{name: "negative minimum", old: "min = 0", new: "min = -100"},
		{name: "initial above maximum", old: "initial = 2000000", new: "initial = 4400000", wantError: "throttler.requested_tps: initial=4400000 is outside range min=0 max=4000000 (step=200000)"},
		{name: "off grid initial", old: "initial = 2000000", new: "initial = 2000001", wantError: "throttler.requested_tps: initial=2000001 is not aligned to step=200000 from min=0 (max=4000000)"},
		{name: "invalid unit", old: "unit = \"transactions/s\"", new: "unit = \"batches/s\""},
		{name: "invalid mutability", old: "[throttler.requested_tps]\ninitial = 2000000\nmin = 0\nmax = 4000000\nstep = 200000\nunit = \"transactions/s\"\nmutability = \"immediate\"", new: "[throttler.requested_tps]\ninitial = 2000000\nmin = 0\nmax = 4000000\nstep = 200000\nunit = \"transactions/s\"\nmutability = \"unsupported-mode\""},
		{name: "duplicate mode", old: "[true, false]", new: "[true, true]"},
		{name: "quoted boolean", old: "[throttler.installed]\ninitial = true", new: "[throttler.installed]\ninitial = \"false\""},
		{name: "number boolean", old: "[throttler.installed]\ninitial = true", new: "[throttler.installed]\ninitial = 1"},
		{name: "quoted allowed", old: "allowed = [true, false]", new: "allowed = [\"true\", \"false\"]"},
		{name: "numeric allowed", old: "allowed = [true, false]", new: "allowed = [1, 0]"},
		{name: "legacy section", old: "[throttler.installed]", new: "[throttler.installation_mode]"},
		{name: "unknown mode", old: "initial = true\nallowed", new: "initial = \"unknown\"\nallowed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contents := strings.Replace(testConfigContents(), test.old, test.new, 1)
			if contents == testConfigContents() {
				t.Fatalf("test replacement %q did not apply", test.old)
			}
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadConfig(path); err == nil {
				t.Fatal("loadConfig accepted invalid throttler config")
			} else if test.wantError != "" && !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("loadConfig error = %q, want detail %q", err, test.wantError)
			}
		})
	}
}

func TestLoadConfigPreservesRetryDelays(t *testing.T) {
	sender := testConfig(t).Sender
	if got, want := sender.Retry.DelaysMS, []int{250, 500, 1000, 2000, 5000}; !slices.Equal(got, want) {
		t.Fatalf("retry delays=%v,want %v", got, want)
	}
}

func TestLoadConfigValidatesWorkerRanges(t *testing.T) {
	for _, section := range []string{"reader.workers", "sender.workers"} {
		t.Run(section, func(t *testing.T) {
			for _, test := range []struct {
				name                    string
				initial, min, max, step int
				unit, mutability        string
				wantErr                 bool
			}{
				{name: "minimum inclusive", initial: 2, min: 2, max: 8, step: 3},
				{name: "maximum inclusive", initial: 8, min: 2, max: 8, step: 3},
				{name: "single value", initial: 3, min: 3, max: 3, step: 100},
				{name: "upper bound outside grid", initial: 5, min: 2, max: 10, step: 3},
				{name: "initial below min", initial: 1, min: 2, max: 8, step: 3, wantErr: true},
				{name: "initial above max", initial: 11, min: 2, max: 8, step: 3, wantErr: true},
				{name: "initial off grid", initial: 3, min: 2, max: 8, step: 3, wantErr: true},
				{name: "zero minimum", initial: 1, min: 0, max: 8, step: 1, wantErr: true},
				{name: "negative minimum", initial: 1, min: -1, max: 8, step: 1, wantErr: true},
				{name: "reversed bounds", initial: 2, min: 2, max: 1, step: 1, wantErr: true},
				{name: "zero step", initial: 2, min: 2, max: 8, step: 0, wantErr: true},
				{name: "negative step", initial: 2, min: 2, max: 8, step: -1, wantErr: true},
				{name: "wrong unit", initial: 2, min: 2, max: 8, step: 1, unit: "transactions", wantErr: true},
				{name: "wrong mutability", initial: 2, min: 2, max: 8, step: 1, mutability: startupOnly, wantErr: true},
			} {
				t.Run(test.name, func(t *testing.T) {
					unit, mutability := workersUnit, immediate
					if test.unit != "" {
						unit = test.unit
					}
					if test.mutability != "" {
						mutability = test.mutability
					}
					initial, max := 1, 7
					if section == "sender.workers" {
						initial, max = 32, 32
					}
					old := fmt.Sprintf("[%s]\ninitial = %d\nmin = 1\nmax = %d\nstep = 1\nunit = \"workers\"\nmutability = \"immediate\"", section, initial, max)
					replacement := fmt.Sprintf("[%s]\ninitial = %d\nmin = %d\nmax = %d\nstep = %d\nunit = %q\nmutability = %q", section, test.initial, test.min, test.max, test.step, unit, mutability)
					contents := strings.Replace(testConfigContents(), old, replacement, 1)
					if contents == testConfigContents() {
						t.Fatal("replacement did not apply")
					}
					path := filepath.Join(t.TempDir(), "config.toml")
					if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
						t.Fatal(err)
					}
					_, err := loadConfig(path)
					if (err != nil) != test.wantErr {
						t.Fatalf("loadConfig error=%v,wantErr=%t", err, test.wantErr)
					}
				})
			}
		})
	}
}

func TestLoadConfigPublishesCustomWorkerProfile(t *testing.T) {
	contents := strings.Replace(testConfigContents(), "[reader.workers]\ninitial = 1\nmin = 1\nmax = 7\nstep = 1", "[reader.workers]\ninitial = 5\nmin = 2\nmax = 11\nstep = 3", 1)
	contents = strings.Replace(contents, "[sender.workers]\ninitial = 32\nmin = 1\nmax = 32\nstep = 1", "[sender.workers]\ninitial = 7\nmin = 3\nmax = 10\nstep = 2", 1)
	contents = strings.Replace(contents, "jitter_percent = 20", "jitter_percent = 50", 1)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	state := newControlState(loaded, zap.NewNop())
	data, err := json.Marshal(state.snapshotAt(nil, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	var snapshot runtimeSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	reader := rangeConfig{Initial: 5, Min: 2, Max: 11, Step: 3, Unit: workersUnit, Mutability: immediate}
	sender := rangeConfig{Initial: 7, Min: 3, Max: 10, Step: 2, Unit: workersUnit, Mutability: immediate}
	if snapshot.Config.ReaderWorkers != reader || snapshot.Config.SenderWorkers != sender ||
		snapshot.Reader.Workers != 5 || snapshot.Sender.Workers != 7 || loaded.Sender.Retry.JitterPercent != 50 {
		t.Fatalf("custom profile lost: snapshot=%+v retry=%+v", snapshot, loaded.Sender.Retry)
	}
}

func TestLoadConfigRejectsUnsupportedSchemas(t *testing.T) {
	for _, version := range []int{0, 1, 2, 4} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			contents := strings.Replace(testConfigContents(), "schema_version = 3", fmt.Sprintf("schema_version = %d", version), 1)
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadConfig(path); err == nil || !strings.Contains(err.Error(), "schema_version") {
				t.Fatalf("unsupported schema error=%v", err)
			}
		})
	}
}

func TestLoadConfigRejectsInvalidSenderConfig(t *testing.T) {
	tests := []struct{ name, old, replacement string }{
		{"missing workers", "[sender.workers]\ninitial = 32", "[sender.workers]"},
		{"initial above maximum", "max = 32", "max = 31"},
		{"empty retry delays", "delays_ms = [250, 500, 1000, 2000, 5000]", "delays_ms = []"},
		{"decreasing retry delays", "delays_ms = [250, 500, 1000, 2000, 5000]", "delays_ms = [250, 100]"},
		{"zero retry delay", "delays_ms = [250, 500, 1000, 2000, 5000]", "delays_ms = [0]"},
		{"missing retry mutability", "[sender.retry]\ndelays_ms = [250, 500, 1000, 2000, 5000]\njitter_percent = 20\nmutability = \"startup-only\"", "[sender.retry]\ndelays_ms = [250, 500, 1000, 2000, 5000]\njitter_percent = 20"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contents := strings.Replace(testConfigContents(), test.old, test.replacement, 1)
			if contents == testConfigContents() {
				t.Fatal("test replacement did not apply")
			}
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadConfig(path); err == nil {
				t.Fatal("loadConfig accepted invalid Sender config")
			}
		})
	}
}

func TestSenderRetryConfigDurationBounds(t *testing.T) {
	for _, jitter := range []int{0, 20, 50} {
		t.Run(fmt.Sprintf("jitter %d", jitter), func(t *testing.T) {
			maximumDelayMS := senderRetryMaxDelayMS(jitter)
			for _, test := range []struct {
				name    string
				delayMS int
				wantErr bool
			}{
				{name: "minimum valid", delayMS: 1},
				{name: "maximum valid", delayMS: int(maximumDelayMS)},
				{name: "duration overflow", delayMS: int(maximumDelayMS + 1), wantErr: true},
				{name: "milliseconds conversion overflow", delayMS: int(math.MaxInt64/int64(time.Millisecond) + 1), wantErr: true},
			} {
				t.Run(test.name, func(t *testing.T) {
					config := senderRetryConfig{DelaysMS: []int{test.delayMS}, JitterPercent: jitter, Mutability: startupOnly}
					if err := config.validate(); (err != nil) != test.wantErr {
						t.Fatalf("validate error=%v,wantErr=%t", err, test.wantErr)
					}
				})
			}
		})
	}
	for _, jitter := range []int{-1, 51} {
		config := senderRetryConfig{DelaysMS: []int{1}, JitterPercent: jitter, Mutability: startupOnly}
		if err := config.validate(); err == nil {
			t.Errorf("accepted jitter %d", jitter)
		}
	}
}
