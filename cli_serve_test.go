package main

import (
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
		{name: "unexpected command", args: []string{"status", "extra"}, wantErr: "unexpected command \"status\""},
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

func TestNewServeStateStartsIdleWithLoadedPolicy(t *testing.T) {
	loadedPolicy := testPolicy(t)
	state := newServeState(loadedPolicy, zap.NewNop())

	if got := state.run.lifecycle.currentState(); got != runStateIdle {
		t.Fatalf("initial lifecycle state = %q, want %q", got, runStateIdle)
	}
	if state.controls.policy.Source.Path != loadedPolicy.Source.Path {
		t.Fatal("serve state did not retain loaded policy")
	}
}

func TestDefaultServeUsesCurrentDefaultConfig(t *testing.T) {
	command, err := parseCLI(nil)
	if err != nil {
		t.Fatalf("parse default CLI: %v", err)
	}
	_, configPath, err := loadPolicy(command.configPath)
	if err != nil {
		t.Fatalf("load default serve policy: %v", err)
	}
	wantConfigPath, err := filepath.Abs(defaultConfigPath)
	if err != nil {
		t.Fatalf("make default config path absolute: %v", err)
	}
	if configPath != wantConfigPath {
		t.Fatalf("config path = %q, want %q", configPath, wantConfigPath)
	}
}
