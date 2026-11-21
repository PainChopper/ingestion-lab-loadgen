package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLoadConfigRejectsUnknownNames(t *testing.T) {
	base := testConfigContents()
	for _, test := range []struct{ name, contents, unknown string }{
		{"empty section", base + "\n[unexpected]\n", "unexpected"},
		{"wrong-case section", strings.Replace(base, "[http]", "[HTTP]", 1), "HTTP"},
		{"wrong-case key", strings.Replace(base, "addr =", "Addr =", 1), "Addr"},
		{"additional wrong-case section", base + "\n[HTTP]\naddr = \"127.0.0.1:8080\"\n", "HTTP"},
		{"additional wrong-case key", strings.Replace(base, "[http]", "[http]\nAddr = \"127.0.0.1:8080\"", 1), "Addr"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := loadConfig(writeConfigInput(t, test.contents))
			if err == nil || !strings.Contains(err.Error(), "invalid keys: "+test.unknown) {
				t.Fatalf("error = %v, want unknown key %q", err, test.unknown)
			}
		})
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
	tests := []struct {
		name     string
		contents string
		wantURL  string
		wantErr  bool
	}{
		{name: "default", contents: contents, wantURL: testSenderURL},
		{name: "external HTTPS", contents: strings.Replace(contents, testSenderURL, "https://ingest.example.test/v1/batches", 1), wantURL: "https://ingest.example.test/v1/batches"},
		{name: "present empty URL", contents: strings.Replace(contents, testSenderURL, "", 1), wantErr: true},
		{name: "malformed URL", contents: strings.Replace(contents, testSenderURL, "http://example.test/%invalid", 1), wantErr: true},
		{name: "relative URL", contents: strings.Replace(contents, testSenderURL, "/internal/ingest", 1), wantErr: true},
		{name: "unsupported scheme", contents: strings.Replace(contents, testSenderURL, "ftp://ingest.example.test/batches", 1), wantErr: true},
		{name: "missing host", contents: strings.Replace(contents, testSenderURL, "http:/internal/ingest", 1), wantErr: true},
		{name: "fragment", contents: strings.Replace(contents, testSenderURL, "https://ingest.example.test/batches#fragment", 1), wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(test.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			loaded, err := loadConfig(path)
			if test.wantErr {
				if err == nil || !strings.Contains(err.Error(), "sender: api:") {
					t.Fatalf("loadConfig error = %v, want sender API error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("loadConfig: %v", err)
			}
			if loaded.Sender.API.URL != test.wantURL {
				t.Fatalf("sender API URL = %q, want %q", loaded.Sender.API.URL, test.wantURL)
			}
		})
	}
}

func TestLoadConfigValidatesLoggingLevel(t *testing.T) {
	for _, level := range []string{"debug", "info", "warn", "error", "verbose"} {
		t.Run(level, func(t *testing.T) {
			input := strings.Replace(testConfigContents(), `level = "info"`, fmt.Sprintf("level = %q", level), 1)
			loaded, err := loadConfig(writeConfigInput(t, input))
			if level == "verbose" {
				if err == nil || !strings.Contains(err.Error(), "logging: level must be debug, info, warn, or error") {
					t.Fatalf("loadConfig error = %v, want logging level error", err)
				}
			} else if err != nil || loaded.Logging.Level != level {
				t.Fatalf("logging level = %q, error = %v, want %q", loaded.Logging.Level, err, level)
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
		wantErr string
	}{
		{name: "minimum inclusive", line: "timeout_ms = 1\n"},
		{name: "integral float", line: "timeout_ms = 1.0\n"},
		{name: "maximum inclusive", line: fmt.Sprintf("timeout_ms = %d\n", maxMS)},
		{name: "missing", wantErr: "timeout_ms"},
		{name: "zero", line: "timeout_ms = 0\n", wantErr: "timeout_ms"},
		{name: "negative", line: "timeout_ms = -1\n", wantErr: "timeout_ms"},
		{name: "overflow", line: fmt.Sprintf("timeout_ms = %d\n", maxMS+1), wantErr: "timeout_ms"},
		{name: "fraction", line: "timeout_ms = 1.9\n", wantErr: "timeout_ms"},
		{name: "string", line: "timeout_ms = \"1\"\n", wantErr: "timeout_ms"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := strings.Replace(contents, "[sender.api]\n", "[sender.api]\n"+test.line, 1)
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

func TestLoadConfigPreservesRetryDelays(t *testing.T) {
	sender := testConfig(t).Sender
	if got, want := sender.Retry.DelaysMS, []int{250, 500, 1000, 2000, 5000}; !slices.Equal(got, want) {
		t.Fatalf("retry delays=%v,want %v", got, want)
	}
}

func TestLoadConfigRejectsInvalidSenderConfig(t *testing.T) {
	tests := []struct{ name, old, replacement string }{
		{"empty retry delays", "delays_ms = [250, 500, 1000, 2000, 5000]", "delays_ms = []"},
		{"decreasing retry delays", "delays_ms = [250, 500, 1000, 2000, 5000]", "delays_ms = [250, 100]"},
		{"zero retry delay", "delays_ms = [250, 500, 1000, 2000, 5000]", "delays_ms = [0]"},
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
			maximumDelayMS := int64(math.MaxInt64) / (int64(time.Millisecond) * int64(100+jitter) / 100)
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
					config := senderRetryConfig{DelaysMS: []int{test.delayMS}, JitterPercent: jitter}
					if err := config.validate(); (err != nil) != test.wantErr {
						t.Fatalf("validate error=%v,wantErr=%t", err, test.wantErr)
					}
				})
			}
		})
	}
	for _, jitter := range []int{-1, 51} {
		t.Run(fmt.Sprintf("invalid jitter %d", jitter), func(t *testing.T) {
			config := senderRetryConfig{DelaysMS: []int{1}, JitterPercent: jitter}
			if err := config.validate(); err == nil {
				t.Errorf("accepted jitter %d", jitter)
			}
		})
	}
}

func TestLoadConfigRequiredFields(t *testing.T) {
	section := ""
	for _, line := range strings.Split(testConfigContents(), "\n") {
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		if !strings.Contains(line, " = ") || strings.HasPrefix(line, "#") {
			continue
		}
		t.Run(section+"/"+strings.Split(line, " = ")[0], func(t *testing.T) {
			// Remove precisely one key, preserving all other required fields.
			input := strings.Replace(testConfigContents(), line+"\n", "", 1)
			if _, err := loadConfig(writeConfigInput(t, input)); err == nil {
				t.Fatalf("accepted missing %s %s", section, line)
			}
		})
	}
}

func TestLoadConfigStrictShapeAndTypes(t *testing.T) {
	for _, tc := range []struct{ name, old, replacement string }{
		{"relative source", `C:\dataset\*.parquet`, "data/*.parquet"},
		{"worker step", "[sender.workers]", "[sender.workers]\nstep = 1"},
		{"unknown", "[logging]", "[logging]\nextra = 1"},
		{"capacity string", "capacity = 2", "capacity = '2'"},
		{"capacity bool", "capacity = 2", "capacity = true"},
		{"batch fraction", "read_batch_size = 1000", "read_batch_size = 1500.5"},
		{"bool string", "installed = true", "installed = 'false'"},
		{"bool number", "installed = true", "installed = 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := strings.Replace(testConfigContents(), tc.old, tc.replacement, 1)
			if input == testConfigContents() {
				t.Fatal("mutation did not apply")
			}
			if _, err := loadConfig(writeConfigInput(t, input)); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}

func TestLoadConfigScalarValuesAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name, old, replacement string
		valid                  bool
	}{
		{"batch1500", "read_batch_size = 1000", "read_batch_size = 1500", true},
		{"batch zero", "read_batch_size = 1000", "read_batch_size = 0", false},
		{"batch negative", "read_batch_size = 1000", "read_batch_size = -1", false},
		{"pacing max", "read_batch_size = 1000", fmt.Sprintf("read_batch_size = %d", math.MaxInt64/int64(time.Second)), true},
		{"pacing overflow", "read_batch_size = 1000", fmt.Sprintf("read_batch_size = %d", math.MaxInt64/int64(time.Second)+1), false},
		{"reader zero", "[readerChannel]\ncapacity = 2", "[readerChannel]\ncapacity = 0", true},
		{"reader3", "[readerChannel]\ncapacity = 2", "[readerChannel]\ncapacity = 3", true},
		{"sender3", "[senderChannel]\ncapacity = 0", "[senderChannel]\ncapacity = 3", true},
		{"reader negative", "[readerChannel]\ncapacity = 2", "[readerChannel]\ncapacity = -1", false},
		{"sender negative", "[senderChannel]\ncapacity = 0", "[senderChannel]\ncapacity = -1", false},
		{"depth max", "capacity = 2", fmt.Sprintf("capacity = %d", maxSnapshotInteger/1000), true},
		{"depth overflow", "capacity = 2", fmt.Sprintf("capacity = %d", maxSnapshotInteger/1000+1), false},
		{"false", "installed = true", "installed = false", true},
		{"TPS0", "initial = 2000000", "initial = 0", true},
		{"TPS off grid", "initial = 2000000", "initial = 2000001", false},
		{"TPS negative", "initial = 2000000", "initial = -200000", false},
		{"TPS high", "initial = 2000000", "initial = 4200000", false},
		{"TPS max off grid", "max = 4000000", "max = 4000001", true},
		{"TPS step0", "step = 200000", "step = 0", false},
		{"TPS max0", "max = 4000000", "max = 0", false},
		{"TPS max negative", "max = 4000000", "max = -1", false},
		{"TPS step negative", "step = 200000", "step = -1", false},
		{"TPS safe max and step", "initial = 2000000\nmax = 4000000\nstep = 200000", fmt.Sprintf("initial = 0\nmax = %d\nstep = %d", maxSnapshotInteger, maxSnapshotInteger), true},
		{"reader workers zero initial", "[reader.workers]\ninitial = 1\nmax = 7", "[reader.workers]\ninitial = 0\nmax = 7", false},
		{"reader workers negative initial", "[reader.workers]\ninitial = 1\nmax = 7", "[reader.workers]\ninitial = -1\nmax = 7", false},
		{"reader workers above max", "[reader.workers]\ninitial = 1\nmax = 7", "[reader.workers]\ninitial = 8\nmax = 7", false},
		{"reader workers zero max", "[reader.workers]\ninitial = 1\nmax = 7", "[reader.workers]\ninitial = 1\nmax = 0", false},
		{"reader workers negative max", "[reader.workers]\ninitial = 1\nmax = 7", "[reader.workers]\ninitial = 1\nmax = -1", false},
		{"reader workers equal max", "[reader.workers]\ninitial = 1\nmax = 7", "[reader.workers]\ninitial = 7\nmax = 7", true},
		{"reader workers unsafe max", "[reader.workers]\ninitial = 1\nmax = 7", fmt.Sprintf("[reader.workers]\ninitial = 1\nmax = %d", maxSnapshotInteger+1), false},
		{"reader workers safe max", "[reader.workers]\ninitial = 1\nmax = 7", fmt.Sprintf("[reader.workers]\ninitial = 1\nmax = %d", maxSnapshotInteger), true},
		{"reader workers custom", "[reader.workers]\ninitial = 1\nmax = 7", "[reader.workers]\ninitial = 33\nmax = 112", true},
		{"sender workers zero initial", "[sender.workers]\ninitial = 32\nmax = 32", "[sender.workers]\ninitial = 0\nmax = 32", false},
		{"sender workers negative initial", "[sender.workers]\ninitial = 32\nmax = 32", "[sender.workers]\ninitial = -1\nmax = 32", false},
		{"sender workers above max", "[sender.workers]\ninitial = 32\nmax = 32", "[sender.workers]\ninitial = 33\nmax = 32", false},
		{"sender workers zero max", "[sender.workers]\ninitial = 32\nmax = 32", "[sender.workers]\ninitial = 1\nmax = 0", false},
		{"sender workers negative max", "[sender.workers]\ninitial = 32\nmax = 32", "[sender.workers]\ninitial = 1\nmax = -1", false},
		{"sender workers equal max", "[sender.workers]\ninitial = 32\nmax = 32", "[sender.workers]\ninitial = 7\nmax = 7", true},
		{"sender workers unsafe max", "[sender.workers]\ninitial = 32\nmax = 32", fmt.Sprintf("[sender.workers]\ninitial = 1\nmax = %d", maxSnapshotInteger+1), false},
		{"sender workers safe max", "[sender.workers]\ninitial = 32\nmax = 32", fmt.Sprintf("[sender.workers]\ninitial = 1\nmax = %d", maxSnapshotInteger), true},
		{"sender workers custom", "[sender.workers]\ninitial = 32\nmax = 32", "[sender.workers]\ninitial = 33\nmax = 112", true},
		{"unsafe capacity", "capacity = 2", fmt.Sprintf("capacity = %d", maxSnapshotInteger+1), false},
		{"unsafe TPS", "max = 4000000", fmt.Sprintf("max = %d", maxSnapshotInteger+1), false},
		{"unsafe step", "initial = 2000000\nmax = 4000000\nstep = 200000", fmt.Sprintf("initial = 0\nmax = 4000000\nstep = %d", maxSnapshotInteger+1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := strings.Replace(testConfigContents(), tc.old, tc.replacement, 1)
			if input == testConfigContents() {
				t.Fatal("mutation did not apply")
			}
			_, err := loadConfig(writeConfigInput(t, input))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t error=%v", tc.valid, err)
			}
		})
	}
}

func TestLoadConfigCheckedInProfile(t *testing.T) {
	cfg, err := loadConfig(defaultConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Reader.ReadBatchSize != 1000 || cfg.ReaderChannel.Capacity != 2 || cfg.SenderChannel.Capacity != 0 ||
		cfg.Reader.Workers != (workersConfig{Initial: 1, Max: 7}) || cfg.Sender.Workers != (workersConfig{Initial: 32, Max: 32}) ||
		cfg.Throttler.RequestedTPS != (tpsConfig{Initial: 2000000, Max: 4000000, Step: 200000}) || !cfg.Throttler.Installed {
		t.Fatalf("example profile changed: %+v", cfg)
	}
}
