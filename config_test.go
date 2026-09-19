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
		"schema_version = 1", "", "[source]", "path = 'C:\\dataset\\*.parquet'", "unit = \"glob-pattern\"", "mutability = \"startup-only\"", "",
		"[reader.read_batch_size]", "default = 50000", "min = 1000", "max = 100000", "step = 1000", "unit = \"transactions\"", "mutability = \"idle-only\"", "",
		"[queue1.capacity]", "default = 2", "allowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]", "unit = \"batches\"", "mutability = \"idle-only\"",
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
			name:     "missing queue capacity default",
			contents: strings.Replace(testConfigContents(), "default = 2\nallowed", "allowed", 1),
			wantErr:  true,
		},
		{
			name:     "explicit zero queue capacity default",
			contents: strings.Replace(testConfigContents(), "default = 2", "default = 0", 1),
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
