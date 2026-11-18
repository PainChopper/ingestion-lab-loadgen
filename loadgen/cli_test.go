package main

import (
	"strings"
	"testing"
)

func TestParseRemoteCLI(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantAction string
		wantURL    string
		wantHelp   bool
		wantErr    bool
	}{
		{name: "status custom URL", args: []string{"status", "--url", "https://example.test/"}, wantAction: "status", wantURL: "https://example.test"},
		{name: "remote help", args: []string{"status", "--help"}, wantAction: "status", wantURL: defaultCLIBaseURL, wantHelp: true},
		{name: "unexpected operand", args: []string{"status", "now"}, wantErr: true},
		{name: "unsupported watch", args: []string{"status", "--watch"}, wantErr: true},
		{name: "invalid scheme", args: []string{"status", "--url", "ftp://example.test"}, wantErr: true},
		{name: "missing URL", args: []string{"status", "--url"}, wantErr: true},
		{name: "path URL", args: []string{"status", "--url", "http://example.test/base"}, wantErr: true},
		{name: "query URL", args: []string{"status", "--url", "http://example.test/?x=1"}, wantErr: true},
		{name: "userinfo URL", args: []string{"status", "--url", "http://user@example.test"}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command, err := parseCLI(test.args)
			if test.wantErr {
				if err == nil {
					t.Fatal("parseCLI returned nil error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCLI(%q): %v", test.args, err)
			}
			if command.remoteAction != test.wantAction || command.remoteURL != test.wantURL || command.showUsage != test.wantHelp {
				t.Fatalf("command = %+v", command)
			}
		})
	}
}

func TestParseSetCLI(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantTarget string
		wantAction string
		wantValue  any
		wantURL    string
		wantHelp   bool
		wantErr    bool
	}{
		{name: "reader workers", args: []string{"set", "readers", "3"}, wantTarget: "readers", wantAction: "set-reader-workers", wantValue: 3, wantURL: defaultCLIBaseURL},
		{name: "sender workers custom URL", args: []string{"set", "senders", "4", "--url", "https://example.test/"}, wantTarget: "senders", wantAction: "set-sender-workers", wantValue: 4, wantURL: "https://example.test"},
		{name: "requested TPS", args: []string{"set", "tps", "0"}, wantTarget: "tps", wantAction: "set-requested-tps", wantValue: 0, wantURL: defaultCLIBaseURL},
		{name: "throttler mode", args: []string{"set", "throttle", "false"}, wantTarget: "throttle", wantAction: "set-throttler-installed", wantValue: false, wantURL: defaultCLIBaseURL},
		{name: "throttler true", args: []string{"set", "throttle", "true"}, wantTarget: "throttle", wantAction: "set-throttler-installed", wantValue: true, wantURL: defaultCLIBaseURL},
		{name: "bool alias", args: []string{"set", "throttle", "1"}, wantErr: true},
		{name: "old value", args: []string{"set", "throttle", "bypass"}, wantErr: true},
		{name: "set help", args: []string{"set", "--help"}, wantHelp: true, wantURL: defaultCLIBaseURL},
		{name: "target help", args: []string{"set", "readers", "--help"}, wantHelp: true, wantURL: defaultCLIBaseURL},
		{name: "unknown target", args: []string{"set", "other", "1"}, wantErr: true},
		{name: "unknown target help", args: []string{"set", "other", "--help"}, wantErr: true},
		{name: "empty numeric value", args: []string{"set", "readers", ""}, wantErr: true},
		{name: "empty bool value", args: []string{"set", "throttle", ""}, wantErr: true},
		{name: "missing value", args: []string{"set", "readers"}, wantErr: true},
		{name: "non integer", args: []string{"set", "tps", "not-an-integer"}, wantErr: true},
		{name: "invalid URL", args: []string{"set", "readers", "2", "--url", "ftp://example.test"}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command, err := parseCLI(test.args)
			if test.wantErr {
				if err == nil {
					t.Fatal("parseCLI returned nil error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCLI(%q): %v", test.args, err)
			}
			if command.remoteAction != "set" || command.remoteSet.target != test.wantTarget || command.remoteSet.requestAction != test.wantAction || command.remoteSet.value != test.wantValue || command.remoteURL != test.wantURL || command.showUsage != test.wantHelp {
				t.Fatalf("command = %+v", command)
			}
		})
	}
}

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
		{name: "positional config", args: []string{"./custom.toml"}, wantErr: "unexpected command \"./custom.toml\""},
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

func TestDefaultServeUsesCurrentDefaultConfig(t *testing.T) {
	command, err := parseCLI(nil)
	if err != nil {
		t.Fatalf("parse default CLI: %v", err)
	}
	loaded, err := loadConfig(command.configPath)
	if err != nil {
		t.Fatalf("load default serve config: %v", err)
	}
	if loaded.Sender.API.URL != testSenderURL {
		t.Fatalf("default Sender URL = %q, want %q", loaded.Sender.API.URL, testSenderURL)
	}
}
