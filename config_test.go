package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	return controlState{lifecycle: newLifecycle(), policy: testPolicy(t)}
}

func testConfigContents() string {
	return strings.Join([]string{
		"schema_version = 2", "", "[source]", "path = 'C:\\dataset\\*.parquet'", "unit = \"glob-pattern\"", "mutability = \"startup-only\"", "",
		"[reader.read_batch_size]", "default = 50000", "min = 1000", "max = 100000", "step = 1000", "unit = \"transactions\"", "mutability = \"idle-only\"", "",
		"[readerChannel.capacity]", "default = 2", "allowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]", "unit = \"batches\"", "mutability = \"idle-only\"",
		"", "[senderChannel.capacity]", "default = 0", "allowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]", "unit = \"batches\"", "mutability = \"idle-only\"",
		"", "[throttler.requested_tps]", "default = 200", "min = 0", "max = 400", "step = 25", "unit = \"transactions/s\"", "mutability = \"immediate\"",
		"", "[throttler.installation_mode]", "default = \"installed\"", "allowed = [\"installed\", \"bypass\"]", "mutability = \"immediate\"",
	}, "\n")
}

func TestLoadPolicyRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct{ name, contents string }{
		{name: "unknown key", contents: "unknown = true"},
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
		name     string
		contents string
		wantErr  bool
	}{
		{
			name:     "missing reader channel capacity default",
			contents: strings.Replace(testConfigContents(), "default = 2\nallowed", "allowed", 1),
			wantErr:  true,
		},
		{
			name:     "explicit zero reader channel capacity default",
			contents: strings.Replace(testConfigContents(), "default = 2", "default = 0", 1),
		},
		{
			name:     "missing sender channel capacity default",
			contents: strings.Replace(testConfigContents(), "[senderChannel.capacity]\ndefault = 0\n", "[senderChannel.capacity]\n", 1),
			wantErr:  true,
		},
		{
			name:     "explicit zero sender channel capacity default",
			contents: testConfigContents(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(test.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			_, _, err := loadPolicy(path)
			if (err != nil) != test.wantErr {
				t.Fatalf("loadPolicy() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}

func TestLoadPolicyRejectsInvalidSenderChannelCapacityPolicy(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
	}{
		{name: "non-zero default", old: "[senderChannel.capacity]\ndefault = 0", new: "[senderChannel.capacity]\ndefault = 1"},
		{name: "off-list allowed value", old: "[senderChannel.capacity]\ndefault = 0\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]", new: "[senderChannel.capacity]\ndefault = 0\nallowed = [0, 1, 3]"},
		{name: "wrong unit", old: "[senderChannel.capacity]\ndefault = 0\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\nunit = \"batches\"", new: "[senderChannel.capacity]\ndefault = 0\nallowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]\nunit = \"transactions\""},
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
	requested := loaded.Throttler.RequestedTPS
	if requested.Default != 200 || requested.Min != 0 || requested.Max != 400 ||
		requested.Step != 25 || requested.Unit != requestedTPSUnit || requested.Mutability != immediate {
		t.Fatalf("requested TPS policy = %+v", requested)
	}
	mode := loaded.Throttler.InstallationMode
	if mode.Default != throttlerInstalled || !mode.contains(throttlerInstalled) ||
		!mode.contains(throttlerBypass) || mode.Mutability != immediate {
		t.Fatalf("installation mode policy = %+v", mode)
	}
}

func TestCheckedInPolicyLoadsApprovedThrottlerProfile(t *testing.T) {
	loaded, _, err := loadPolicy(defaultConfigPath)
	if err != nil {
		t.Fatalf("load checked-in policy: %v", err)
	}
	if loaded.Throttler.RequestedTPS.Default != 200 || loaded.Throttler.RequestedTPS.Min != 0 ||
		loaded.Throttler.RequestedTPS.Max != 400 || loaded.Throttler.RequestedTPS.Step != 25 ||
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
		{name: "missing TPS default", old: "[throttler.requested_tps]\ndefault = 200", new: "[throttler.requested_tps]"},
		{name: "missing mode allowed", old: "allowed = [\"installed\", \"bypass\"]", new: ""},
		{name: "unknown key", old: "[throttler.installation_mode]", new: "[throttler.installation_mode]\nextra = true"},
		{name: "negative minimum", old: "min = 0", new: "min = -25"},
		{name: "off grid default", old: "default = 200", new: "default = 201"},
		{name: "invalid unit", old: "unit = \"transactions/s\"", new: "unit = \"batches/s\""},
		{name: "invalid mutability", old: "[throttler.requested_tps]\ndefault = 200\nmin = 0\nmax = 400\nstep = 25\nunit = \"transactions/s\"\nmutability = \"immediate\"", new: "[throttler.requested_tps]\ndefault = 200\nmin = 0\nmax = 400\nstep = 25\nunit = \"transactions/s\"\nmutability = \"idle-only\""},
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
