package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testPolicy(t *testing.T) policy {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(testConfigContents()), 0o600); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	loaded, _, err := loadPolicy(path)
	if err != nil {
		t.Fatalf("load test policy: %v", err)
	}
	return loaded
}

func newTestControlState(t *testing.T) controlState {
	t.Helper()
	loaded := testPolicy(t)
	return controlState{
		metricsWindow: time.Duration(loaded.Metrics.WindowMS.Default) * time.Millisecond,
		run:           controlRunState{lifecycle: newLifecycle()},
		controls:      configuredControls{policy: loaded},
	}
}

func testConfigContents() string {
	return strings.Join([]string{
		"schema_version = 2", "", "[source]", "path = 'C:\\dataset\\*.parquet'", "unit = \"glob-pattern\"", "mutability = \"startup-only\"", "",
		"[reader.read_batch_size]", "default = 1000", "min = 1000", "max = 100000", "step = 1000", "unit = \"transactions\"", "mutability = \"idle-only\"", "",
		"[reader.workers]", "default = 1", "min = 1", "max = 7", "step = 1", "unit = \"workers\"", "mutability = \"immediate\"", "",
		"[readerChannel.capacity]", "default = 2", "allowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]", "unit = \"batches\"", "mutability = \"idle-only\"",
		"", "[senderChannel.capacity]", "default = 0", "allowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]", "unit = \"batches\"", "mutability = \"idle-only\"",
		"", "[throttler.requested_tps]", "default = 2000", "min = 0", "max = 4000", "step = 100", "unit = \"transactions/s\"", "mutability = \"immediate\"",
		"", "[throttler.installation_mode]", "default = \"installed\"", "allowed = [\"installed\", \"bypass\"]", "mutability = \"immediate\"",
		"", "[sender.workers]", "default = 32", "min = 1", "max = 32", "step = 1", "unit = \"workers\"", "mutability = \"immediate\"",
		"", "[sender.simulated.delay_ms]", "default = 10", "min = 0", "max = 2000", "step = 10", "unit = \"milliseconds\"", "mutability = \"immediate\"",
		"", "[sender.simulated.error_rate_percent]", "default = 2", "min = 0", "max = 100", "step = 1", "unit = \"percent\"", "mutability = \"immediate\"",
		"", "[sender.retry]", "max_attempts = 3", "backoff_base_ms = 250", "backoff_multiplier = 2", "jitter_percent = 20", "mutability = \"startup-only\"",
		"", "[metrics.window_ms]", "default = 1000", "min = 100", "max = 10000", "step = 100", "unit = \"milliseconds\"", "mutability = \"startup-only\"",
	}, "\n")
}

func TestLoadPolicyRejectsInvalidConfiguration(t *testing.T) {
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
			if _, _, err := loadPolicy(path); err == nil {
				t.Fatal("loadPolicy error = nil")
			}
		})
	}
}

func TestLoadPolicyRequiresExplicitPolicyFields(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
	}{
		{name: "schema version", old: "schema_version = 2\n", new: ""},
		{name: "source path", old: "[source]\npath = 'C:\\dataset\\*.parquet'\n", new: "[source]\n"},
		{name: "source unit", old: "path = 'C:\\dataset\\*.parquet'\nunit = \"glob-pattern\"\n", new: "path = 'C:\\dataset\\*.parquet'\n"},
		{name: "source mutability", old: "unit = \"glob-pattern\"\nmutability = \"startup-only\"\n", new: "unit = \"glob-pattern\"\n"},
		{name: "reader batch default", old: "[reader.read_batch_size]\ndefault = 1000\n", new: "[reader.read_batch_size]\n"},
		{name: "reader batch min", old: "default = 1000\nmin = 1000\n", new: "default = 1000\n"},
		{name: "reader batch max", old: "min = 1000\nmax = 100000\n", new: "min = 1000\n"},
		{name: "reader batch step", old: "max = 100000\nstep = 1000\n", new: "max = 100000\n"},
		{name: "reader batch unit", old: "step = 1000\nunit = \"transactions\"\n", new: "step = 1000\n"},
		{name: "reader batch mutability", old: "unit = \"transactions\"\nmutability = \"idle-only\"\n", new: "unit = \"transactions\"\n"},
		{name: "reader channel default", old: "[readerChannel.capacity]\ndefault = 2\n", new: "[readerChannel.capacity]\n"},
		{name: "reader channel allowed", old: "default = 2\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\n", new: "default = 2\n"},
		{name: "reader channel unit", old: "allowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\nunit = \"batches\"\n", new: "allowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\n"},
		{name: "reader channel mutability", old: "[readerChannel.capacity]\ndefault = 2\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\nunit = \"batches\"\nmutability = \"idle-only\"\n", new: "[readerChannel.capacity]\ndefault = 2\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\nunit = \"batches\"\n"},
		{name: "sender channel default", old: "[senderChannel.capacity]\ndefault = 0\n", new: "[senderChannel.capacity]\n"},
		{name: "sender channel allowed", old: "[senderChannel.capacity]\ndefault = 0\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\n", new: "[senderChannel.capacity]\ndefault = 0\n"},
		{name: "sender channel unit", old: "[senderChannel.capacity]\ndefault = 0\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\nunit = \"batches\"\n", new: "[senderChannel.capacity]\ndefault = 0\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\n"},
		{name: "sender channel mutability", old: "[senderChannel.capacity]\ndefault = 0\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\nunit = \"batches\"\nmutability = \"idle-only\"\n", new: "[senderChannel.capacity]\ndefault = 0\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\nunit = \"batches\"\n"},
		{name: "requested TPS default", old: "[throttler.requested_tps]\ndefault = 2000\n", new: "[throttler.requested_tps]\n"},
		{name: "requested TPS min", old: "default = 2000\nmin = 0\n", new: "default = 2000\n"},
		{name: "requested TPS max", old: "min = 0\nmax = 4000\n", new: "min = 0\n"},
		{name: "requested TPS step", old: "max = 4000\nstep = 100\n", new: "max = 4000\n"},
		{name: "requested TPS unit", old: "step = 100\nunit = \"transactions/s\"\n", new: "step = 100\n"},
		{name: "requested TPS mutability", old: "unit = \"transactions/s\"\nmutability = \"immediate\"\n", new: "unit = \"transactions/s\"\n"},
		{name: "installation mode default", old: "[throttler.installation_mode]\ndefault = \"installed\"\n", new: "[throttler.installation_mode]\n"},
		{name: "installation mode allowed", old: "default = \"installed\"\nallowed = [\"installed\", \"bypass\"]\n", new: "default = \"installed\"\n"},
		{name: "installation mode mutability", old: "allowed = [\"installed\", \"bypass\"]\nmutability = \"immediate\"", new: "allowed = [\"installed\", \"bypass\"]\n"},
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
			if _, _, err := loadPolicy(path); err == nil {
				t.Fatal("loadPolicy accepted config with a missing policy field")
			}
		})
	}
}

func TestLoadPolicyAllowsExplicitZeroValues(t *testing.T) {
	tests := []struct {
		name        string
		contents    string
		mustContain string
	}{
		{
			name:        "reader channel capacity default",
			contents:    strings.Replace(testConfigContents(), "[readerChannel.capacity]\ndefault = 2\n", "[readerChannel.capacity]\ndefault = 0\n", 1),
			mustContain: "[readerChannel.capacity]\ndefault = 0\n",
		},
		{
			name:        "sender channel capacity default",
			contents:    testConfigContents(),
			mustContain: "[senderChannel.capacity]\ndefault = 0\n",
		},
		{
			name:        "requested TPS minimum",
			contents:    testConfigContents(),
			mustContain: "[throttler.requested_tps]\ndefault = 2000\nmin = 0\n",
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
			if _, _, err := loadPolicy(path); err != nil {
				t.Fatalf("loadPolicy explicit zero: %v", err)
			}
		})
	}
}

func TestLoadPolicyAllowsSenderChannelCapacityPolicy(t *testing.T) {
	contents := strings.Replace(
		testConfigContents(),
		"[senderChannel.capacity]\ndefault = 0\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]",
		"[senderChannel.capacity]\ndefault = 3\nallowed = [0, 3, 7]",
		1,
	)
	if contents == testConfigContents() {
		t.Fatal("sender channel capacity policy replacement did not apply")
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadPolicy(path); err != nil {
		t.Fatalf("loadPolicy sender channel capacity policy: %v", err)
	}
}

func TestLoadPolicyRejectsInvalidSenderChannelCapacityPolicy(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
	}{
		{name: "wrong unit", old: "[senderChannel.capacity]\ndefault = 0\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\nunit = \"batches\"", new: "[senderChannel.capacity]\ndefault = 0\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\nunit = \"transactions\""},
		{name: "wrong mutability", old: "[senderChannel.capacity]\ndefault = 0\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\nunit = \"batches\"\nmutability = \"idle-only\"", new: "[senderChannel.capacity]\ndefault = 0\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\nunit = \"batches\"\nmutability = \"immediate\""},
		{name: "negative allowed value", old: "allowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]", new: "allowed = [-1, 0, 1]"},
		{name: "not strictly increasing allowed values", old: "allowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]", new: "allowed = [0, 2, 1]"},
		{name: "default outside allowed", old: "[senderChannel.capacity]\ndefault = 0", new: "[senderChannel.capacity]\ndefault = 3"},
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
			if _, _, err := loadPolicy(path); err == nil {
				t.Fatal("loadPolicy accepted invalid sender channel capacity policy")
			}
		})
	}
}

func TestThrottlerPolicyUsesApprovedProfile(t *testing.T) {
	loaded := testPolicy(t)
	reader := loaded.Reader.ReadBatchSize
	if reader.Default != 1_000 || reader.Min != 1_000 || reader.Max != 100_000 ||
		reader.Step != 1_000 || reader.Unit != batchSizeUnit || reader.Mutability != idleOnly {
		t.Fatalf("reader batch size policy = %+v", reader)
	}
	requested := loaded.Throttler.RequestedTPS
	if requested.Default != 2_000 || requested.Min != 0 || requested.Max != 4_000 ||
		requested.Step != 100 || requested.Unit != requestedTPSUnit || requested.Mutability != immediate {
		t.Fatalf("requested TPS policy = %+v", requested)
	}
	mode := loaded.Throttler.InstallationMode
	if mode.Default != throttlerInstalled || !mode.contains(throttlerInstalled) ||
		!mode.contains(throttlerBypass) || mode.Mutability != immediate {
		t.Fatalf("installation mode policy = %+v", mode)
	}
}

func TestMetricsWindowPolicyUsesApprovedProfile(t *testing.T) {
	window := testPolicy(t).Metrics.WindowMS
	if window.Default != 1_000 || window.Min != 100 || window.Max != 10_000 ||
		window.Step != 100 || window.Unit != metricsWindowUnit || window.Mutability != startupOnly {
		t.Fatalf("metrics window policy = %+v", window)
	}
}

func TestLoadPolicyRejectsInvalidMetricsWindowPolicy(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
	}{
		{name: "missing default", old: "[metrics.window_ms]\ndefault = 1000", new: "[metrics.window_ms]"},
		{name: "wrong unit", old: "unit = \"milliseconds\"", new: "unit = \"seconds\""},
		{name: "wrong mutability", old: "[metrics.window_ms]\ndefault = 1000\nmin = 100\nmax = 10000\nstep = 100\nunit = \"milliseconds\"\nmutability = \"startup-only\"", new: "[metrics.window_ms]\ndefault = 1000\nmin = 100\nmax = 10000\nstep = 100\nunit = \"milliseconds\"\nmutability = \"immediate\""},
		{name: "below minimum", old: "[metrics.window_ms]\ndefault = 1000\nmin = 100", new: "[metrics.window_ms]\ndefault = 1000\nmin = 99"},
		{name: "above maximum", old: "[metrics.window_ms]\ndefault = 1000\nmin = 100\nmax = 10000", new: "[metrics.window_ms]\ndefault = 1000\nmin = 100\nmax = 10100"},
		{name: "off grid default", old: "[metrics.window_ms]\ndefault = 1000", new: "[metrics.window_ms]\ndefault = 1050"},
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
			if _, _, err := loadPolicy(path); err == nil {
				t.Fatal("loadPolicy accepted invalid metrics window policy")
			}
		})
	}
}

func TestLoadPolicyAllowsSafeMetricsWindowDefaults(t *testing.T) {
	for _, value := range []int{100, 300, 1_000, 10_000} {
		t.Run(fmt.Sprintf("%d milliseconds", value), func(t *testing.T) {
			contents := strings.Replace(
				testConfigContents(),
				"[metrics.window_ms]\ndefault = 1000",
				fmt.Sprintf("[metrics.window_ms]\ndefault = %d", value),
				1,
			)
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := loadPolicy(path); err != nil {
				t.Fatalf("loadPolicy metrics window %d: %v", value, err)
			}
		})
	}
}

func TestCheckedInPolicyLoadsApprovedThrottlerProfile(t *testing.T) {
	loaded, _, err := loadPolicy(defaultConfigPath)
	if err != nil {
		t.Fatalf("load checked-in policy: %v", err)
	}
	if loaded.Reader.ReadBatchSize.Default != 1_000 ||
		loaded.Throttler.RequestedTPS.Default != 2_000 || loaded.Throttler.RequestedTPS.Min != 0 ||
		loaded.Throttler.RequestedTPS.Max != 4_000 || loaded.Throttler.RequestedTPS.Step != 100 ||
		loaded.Throttler.InstallationMode.Default != throttlerInstalled {
		t.Fatalf("checked-in throttler policy = %+v", loaded.Throttler)
	}
}

func TestLoadPolicyRejectsInvalidThrottlerPolicy(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
	}{
		{name: "missing TPS default", old: "[throttler.requested_tps]\ndefault = 2000", new: "[throttler.requested_tps]"},
		{name: "missing mode allowed", old: "allowed = [\"installed\", \"bypass\"]", new: ""},
		{name: "unknown key", old: "[throttler.installation_mode]", new: "[throttler.installation_mode]\nextra = true"},
		{name: "negative minimum", old: "min = 0", new: "min = -100"},
		{name: "off grid default", old: "default = 2000", new: "default = 2001"},
		{name: "invalid unit", old: "unit = \"transactions/s\"", new: "unit = \"batches/s\""},
		{name: "invalid mutability", old: "[throttler.requested_tps]\ndefault = 2000\nmin = 0\nmax = 4000\nstep = 100\nunit = \"transactions/s\"\nmutability = \"immediate\"", new: "[throttler.requested_tps]\ndefault = 2000\nmin = 0\nmax = 4000\nstep = 100\nunit = \"transactions/s\"\nmutability = \"idle-only\""},
		{name: "duplicate mode", old: "[\"installed\", \"bypass\"]", new: "[\"installed\", \"installed\"]"},
		{name: "unknown mode", old: "default = \"installed\"\nallowed", new: "default = \"unknown\"\nallowed"},
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
			if _, _, err := loadPolicy(path); err == nil {
				t.Fatal("loadPolicy accepted invalid throttler policy")
			}
		})
	}
}

func TestSenderPolicyUsesApprovedProfile(t *testing.T) {
	sender := testPolicy(t).Sender
	if sender.Workers != (rangePolicy{Default: 32, Min: 1, Max: 32, Step: 1, Unit: workersUnit, Mutability: immediate}) {
		t.Fatalf("workers policy = %+v", sender.Workers)
	}
	if sender.Simulated.DelayMS != (rangePolicy{Default: 10, Min: 0, Max: 2000, Step: 10, Unit: metricsWindowUnit, Mutability: immediate}) {
		t.Fatalf("delay policy = %+v", sender.Simulated.DelayMS)
	}
	if sender.Simulated.ErrorRatePercent != (rangePolicy{Default: 2, Min: 0, Max: 100, Step: 1, Unit: percentUnit, Mutability: immediate}) {
		t.Fatalf("error rate policy = %+v", sender.Simulated.ErrorRatePercent)
	}
}

func TestReaderWorkersPolicyUsesApprovedProfile(t *testing.T) {
	workers := testPolicy(t).Reader.Workers
	want := rangePolicy{Default: 1, Min: 1, Max: 7, Step: 1, Unit: workersUnit, Mutability: immediate}
	if workers != want || !workers.contains(1) || !workers.contains(7) || workers.contains(0) || workers.contains(8) {
		t.Fatalf("Reader workers policy = %+v, want %+v", workers, want)
	}
}

func TestLoadPolicyRejectsReaderWorkersProfileDrift(t *testing.T) {
	tests := []struct{ name, old, replacement string }{
		{"missing workers", "[reader.workers]\ndefault = 1", "[reader.workers]"},
		{"default zero", "[reader.workers]\ndefault = 1", "[reader.workers]\ndefault = 0"},
		{"minimum zero", "[reader.workers]\ndefault = 1\nmin = 1", "[reader.workers]\ndefault = 1\nmin = 0"},
		{"maximum eight", "[reader.workers]\ndefault = 1\nmin = 1\nmax = 7", "[reader.workers]\ndefault = 1\nmin = 1\nmax = 8"},
		{"step two", "[reader.workers]\ndefault = 1\nmin = 1\nmax = 7\nstep = 1", "[reader.workers]\ndefault = 1\nmin = 1\nmax = 7\nstep = 2"},
		{"wrong mutability", "[reader.workers]\ndefault = 1\nmin = 1\nmax = 7\nstep = 1\nunit = \"workers\"\nmutability = \"immediate\"", "[reader.workers]\ndefault = 1\nmin = 1\nmax = 7\nstep = 1\nunit = \"workers\"\nmutability = \"idle-only\""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contents := strings.Replace(testConfigContents(), test.old, test.replacement, 1)
			if contents == testConfigContents() {
				t.Fatal("replacement did not apply")
			}
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := loadPolicy(path); err == nil {
				t.Fatal("loadPolicy accepted invalid Reader workers policy")
			}
		})
	}
}

func TestLoadPolicyRejectsInvalidSenderPolicy(t *testing.T) {
	tests := []struct{ name, old, replacement string }{
		{"missing workers", "[sender.workers]\ndefault = 32", "[sender.workers]"},
		{"wrong workers max", "max = 32", "max = 33"},
		{"missing delay", "[sender.simulated.delay_ms]\ndefault = 10", "[sender.simulated.delay_ms]"},
		{"wrong delay step", "[sender.simulated.delay_ms]\ndefault = 10\nmin = 0\nmax = 2000\nstep = 10", "[sender.simulated.delay_ms]\ndefault = 10\nmin = 0\nmax = 2000\nstep = 5"},
		{"wrong error default", "[sender.simulated.error_rate_percent]\ndefault = 2", "[sender.simulated.error_rate_percent]\ndefault = 3"},
		{"wrong retry attempts", "max_attempts = 3", "max_attempts = 4"},
		{"missing retry mutability", "[sender.retry]\nmax_attempts = 3\nbackoff_base_ms = 250\nbackoff_multiplier = 2\njitter_percent = 20\nmutability = \"startup-only\"", "[sender.retry]\nmax_attempts = 3\nbackoff_base_ms = 250\nbackoff_multiplier = 2\njitter_percent = 20"},
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
			if _, _, err := loadPolicy(path); err == nil {
				t.Fatal("loadPolicy accepted invalid Sender policy")
			}
		})
	}
}
