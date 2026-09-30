package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestParseCLI(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantConfig string
		wantHelp   bool
		wantErr    string
	}{
		{name: "default serve", wantConfig: ""},
		{name: "serve", args: []string{"serve"}, wantConfig: ""},
		{name: "top level help", args: []string{"--help"}, wantHelp: true},
		{name: "serve help", args: []string{"serve", "--help"}, wantHelp: true},
		{name: "serve with config", args: []string{"serve", "--config", "custom.toml"}, wantConfig: "custom.toml"},
		{name: "legacy config", args: []string{"custom.toml"}, wantConfig: "custom.toml"},
		{name: "missing config value", args: []string{"serve", "--config"}, wantErr: "invalid serve arguments"},
		{name: "unknown serve argument", args: []string{"serve", "--other", "value"}, wantErr: "unexpected argument \"--other\""},
		{name: "invalid status arguments", args: []string{"status", "extra"}, wantErr: "invalid status arguments"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command, err := parseCLI(test.args)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("parseCLI(%q) error = %v, want containing %q", test.args, err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCLI(%q): %v", test.args, err)
			}
			if command.configPath != test.wantConfig {
				t.Fatalf("config path = %q, want %q", command.configPath, test.wantConfig)
			}
			if command.showUsage != test.wantHelp {
				t.Fatalf("show usage = %t, want %t", command.showUsage, test.wantHelp)
			}
		})
	}
}

func TestNewServeStateStartsIdleWithLoadedConfig(t *testing.T) {
	loadedConfig := testConfig(t)
	state := newServeState(loadedConfig, zap.NewNop())

	if got := state.run.lifecycle.currentState(); got != runStateIdle {
		t.Fatalf("initial lifecycle state = %q, want %q", got, runStateIdle)
	}
	if state.controls.config.Source.Path != loadedConfig.Source.Path {
		t.Fatal("serve state did not retain loaded config")
	}
}

func TestDefaultServeUsesCurrentDefaultConfig(t *testing.T) {
	command, err := parseCLI(nil)
	if err != nil {
		t.Fatalf("parse default CLI: %v", err)
	}
	_, err = loadConfig(command.configPath)
	if err != nil {
		t.Fatalf("load default serve config: %v", err)
	}
}

func TestRunServeRunFailsBeforePipelineWhenListenerOccupied(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:8080")
	if err != nil {
		t.Skipf("cannot reserve default HTTP listener: %v", err)
	}
	defer listener.Close()

	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte(testConfigContents()), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := runServe(context.Background(), configPath, true); err == nil || !strings.Contains(err.Error(), "listen HTTP server") {
		t.Fatalf("runServe error = %v, want listener startup failure", err)
	}
}
