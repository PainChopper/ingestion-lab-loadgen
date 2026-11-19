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
	return `schema_version = 4

[http]
addr = "127.0.0.1:8080"

[source]
path = 'C:\dataset\*.parquet'

[reader]
read_batch_size = 1000

[reader.workers]
initial = 1
max = 7

[readerChannel]
capacity = 2

[senderChannel]
capacity = 0

[throttler]
installed = true

[throttler.requested_tps]
initial = 2000000
max = 4000000
step = 200000


[sender.workers]
initial = 32
max = 32

[sender.api]
timeout_ms = 30000
url = "http://127.0.0.1:8081/internal/ingest"

[sender.retry]
# After the final delay, retries continue at that delay until delivery succeeds
# or the operator explicitly stops or resets the run.
delays_ms = [250, 500, 1000, 2000, 5000]
jitter_percent = 20

[metrics]
window_ms = 1000

[logging]
level = "info"
`
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
		{"metrics bool", "[metrics]\nwindow_ms = 1000", "[metrics]\nwindow_ms = true"},
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
		{name: "malformed URL", contents: strings.Replace(contents, testSenderURL, "http://example.test/%invalid", 1), wantErr: true},
		{name: "relative URL", contents: strings.Replace(contents, testSenderURL, "/internal/ingest", 1), wantErr: true},
		{name: "unsupported scheme", contents: strings.Replace(contents, testSenderURL, "ftp://ingest.example.test/batches", 1), wantErr: true},
		{name: "missing host", contents: strings.Replace(contents, testSenderURL, "http:/internal/ingest", 1), wantErr: true},
		{name: "fragment", contents: strings.Replace(contents, testSenderURL, "https://ingest.example.test/batches#fragment", 1), wantErr: true},
		{
			name: "removed API metadata",
			contents: strings.Replace(
				contents,
				urlLine,
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
		{name: "minimum inclusive", line: "timeout_ms = 1\n", version: 4},
		{name: "integral float", line: "timeout_ms = 1.0\n", version: 4},
		{name: "maximum inclusive", line: fmt.Sprintf("timeout_ms = %d\n", maxMS), version: 4},
		{name: "missing", version: 4, wantErr: "timeout_ms"},
		{name: "zero", line: "timeout_ms = 0\n", version: 4, wantErr: "timeout_ms"},
		{name: "negative", line: "timeout_ms = -1\n", version: 4, wantErr: "timeout_ms"},
		{name: "overflow", line: fmt.Sprintf("timeout_ms = %d\n", maxMS+1), version: 4, wantErr: "timeout_ms"},
		{name: "fraction", line: "timeout_ms = 1.9\n", version: 4, wantErr: "timeout_ms"},
		{name: "string", line: "timeout_ms = \"1\"\n", version: 4, wantErr: "timeout_ms"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := strings.Replace(contents, "schema_version = 4", fmt.Sprintf("schema_version = %d", test.version), 1)
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

func TestLoadConfigPreservesRetryDelays(t *testing.T) {
	sender := testConfig(t).Sender
	if got, want := sender.Retry.DelaysMS, []int{250, 500, 1000, 2000, 5000}; !slices.Equal(got, want) {
		t.Fatalf("retry delays=%v,want %v", got, want)
	}
}

func TestLoadConfigRejectsInvalidSenderConfig(t *testing.T) {
	tests := []struct{ name, old, replacement string }{
		{"initial above maximum", "max = 32", "max = 31"},
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
					config := senderRetryConfig{DelaysMS: []int{test.delayMS}, JitterPercent: jitter}
					if err := config.validate(); (err != nil) != test.wantErr {
						t.Fatalf("validate error=%v,wantErr=%t", err, test.wantErr)
					}
				})
			}
		})
	}
	for _, jitter := range []int{-1, 51} {
		config := senderRetryConfig{DelaysMS: []int{1}, JitterPercent: jitter}
		if err := config.validate(); err == nil {
			t.Errorf("accepted jitter %d", jitter)
		}
	}
}

func TestSchema4RequiredFields(t *testing.T) {
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

func writeConfigInput(t *testing.T, input string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSchema4StrictShapeAndTypes(t *testing.T) {
	for _, tc := range []struct{ name, old, replacement string }{
		{"relative source", `C:\dataset\*.parquet`, "data/*.parquet"},
		{"schema3", "schema_version = 4", "schema_version = 3"},
		{"schema2", "schema_version = 4", "schema_version = 2"},
		{"future", "schema_version = 4", "schema_version = 5"},
		{"legacy batch", "[reader]\nread_batch_size = 1000", "[reader.read_batch_size]\ninitial = 1000"},
		{"legacy queue", "[readerChannel]\ncapacity = 2", "[readerChannel.capacity]\ninitial = 2\nallowed = [0, 2]"},
		{"legacy bool", "[throttler]\ninstalled = true", "[throttler.installed]\ninitial = true\nallowed = [true,false]"},
		{"legacy window", "[metrics]\nwindow_ms = 1000", "[metrics.window_ms]\ninitial = 1000"},
		{"worker min", "[reader.workers]", "[reader.workers]\nmin = 1"},
		{"worker step", "[sender.workers]", "[sender.workers]\nstep = 1"},
		{"TPS min", "[throttler.requested_tps]", "[throttler.requested_tps]\nmin = 0"},
		{"unit", "[source]", "[source]\nunit = 'glob-pattern'"},
		{"mutability", "[logging]", "[logging]\nmutability = 'startup-only'"},
		{"unknown", "[metrics]", "[metrics]\nextra = 1"},
		{"capacity string", "capacity = 2", "capacity = '2'"},
		{"capacity bool", "capacity = 2", "capacity = true"},
		{"batch fraction", "read_batch_size = 1000", "read_batch_size = 1500.5"},
		{"window fraction", "window_ms = 1000", "window_ms = 1000.1"},
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

func TestSchema4ScalarValuesAndBounds(t *testing.T) {
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
		{"window100", "window_ms = 1000", "window_ms = 100", true},
		{"window1050", "window_ms = 1000", "window_ms = 1050", true},
		{"window10000", "window_ms = 1000", "window_ms = 10000", true},
		{"window99", "window_ms = 1000", "window_ms = 99", false},
		{"window10001", "window_ms = 1000", "window_ms = 10001", false},
		{"false", "installed = true", "installed = false", true},
		{"TPS0", "initial = 2000000", "initial = 0", true},
		{"TPS off grid", "initial = 2000000", "initial = 2000001", false},
		{"TPS negative", "initial = 2000000", "initial = -200000", false},
		{"TPS high", "initial = 2000000", "initial = 4200000", false},
		{"TPS max off grid", "max = 4000000", "max = 4000001", true},
		{"TPS step0", "step = 200000", "step = 0", false},
		{"TPS max0", "max = 4000000", "max = 0", false},
		{"workers0", "[reader.workers]\ninitial = 1", "[reader.workers]\ninitial = 0", false},
		{"workers high", "[reader.workers]\ninitial = 1", "[reader.workers]\ninitial = 8", false},
		{"unsafe capacity", "capacity = 2", fmt.Sprintf("capacity = %d", maxSnapshotInteger+1), false},
		{"unsafe workers", "max = 7", fmt.Sprintf("max = %d", maxSnapshotInteger+1), false},
		{"unsafe TPS", "max = 4000000", fmt.Sprintf("max = %d", maxSnapshotInteger+1), false},
		{"unsafe step", "initial = 2000000\nmax = 4000000\nstep = 200000", fmt.Sprintf("initial = 0\nmax = 4000000\nstep = %d", maxSnapshotInteger+1), false},
		{"workers max0", "max = 7", "max = 0", false},
		{"workers custom", "[reader.workers]\ninitial = 1\nmax = 7", "[reader.workers]\ninitial = 33\nmax = 112", true},
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

func TestSchema4CheckedInProfileAndHTTPPool(t *testing.T) {
	cfg, err := loadConfig(defaultConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Reader.ReadBatchSize != 1000 || cfg.ReaderChannel.Capacity != 2 || cfg.SenderChannel.Capacity != 0 ||
		cfg.Reader.Workers != (workersConfig{Initial: 1, Max: 7}) || cfg.Sender.Workers != (workersConfig{Initial: 32, Max: 32}) ||
		cfg.Throttler.RequestedTPS != (tpsConfig{Initial: 2000000, Max: 4000000, Step: 200000}) || !cfg.Throttler.Installed || cfg.Metrics.WindowMS != 1000 {
		t.Fatalf("example profile changed: %+v", cfg)
	}
	cfg.Sender.Workers = workersConfig{Initial: 7, Max: 112}
	options := pipelineOptions(cfg, nil)
	if options.SenderMaxConns != 112 || options.Senders != 7 {
		t.Fatalf("pool limit changed: %+v", options)
	}
}
