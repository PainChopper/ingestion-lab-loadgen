package cli

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"
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
		{name: "status default URL", args: []string{"status"}, wantAction: "status", wantURL: defaultCLIBaseURL},
		{name: "malformed URL", args: []string{"status", "--url", "http://example.test/%invalid"}, wantErr: true},
		{name: "empty host URL", args: []string{"status", "--url", "http:///"}, wantErr: true},
		{name: "relative URL", args: []string{"status", "--url", "example.test"}, wantErr: true},
		{name: "fragment URL", args: []string{"status", "--url", "http://example.test/#x"}, wantErr: true},
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
		{name: "missing target", args: []string{"set"}, wantErr: true},
		{name: "wrong URL flag", args: []string{"set", "readers", "2", "--other", "http://example.test"}, wantErr: true},
		{name: "empty URL", args: []string{"set", "readers", "2", "--url", ""}, wantErr: true},
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
		{name: "serve help with extra argument", args: []string{"serve", "--help", "extra"}, wantErr: "invalid serve arguments"},
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

func TestRunCLIExitCodes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--help"}, &stdout, &stderr, nil); code != cliExitSuccess || stdout.String() != cliUsage+"\n" || stderr.Len() != 0 {
		t.Fatalf("help exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code := Run([]string{"inspect"}, &stdout, &stderr, nil)
	if code != cliExitUsage || stdout.Len() != 0 || stderr.String() != "unexpected command \"inspect\"\n"+cliUsage+"\n" {
		t.Fatalf("unknown command exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if got := Run([]string{"status", "--url", "ftp://example.test"}, &stdout, &stderr, nil); got != cliExitUsage {
		t.Fatalf("invalid URL exit = %d, want %d", got, cliExitUsage)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "usage: ingestion-lab-loadgen status [--url <url>]") {
		t.Fatalf("invalid URL stdout/stderr = %q / %q", stdout.String(), stderr.String())
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "conflict", http.StatusConflict)
	}))
	defer server.Close()
	for _, args := range [][]string{{"status", "--url", server.URL}, {"set", "tps", "0", "--url", server.URL}} {
		t.Run(args[0]+" HTTP conflict", func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := Run(args, &stdout, &stderr, nil); got != cliExitHTTP {
				t.Fatalf("HTTP exit = %d, want %d", got, cliExitHTTP)
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), "HTTP 409") {
				t.Fatalf("stdout/stderr = %q / %q", stdout.String(), stderr.String())
			}
		})
	}
	stdout.Reset()
	stderr.Reset()
	if got := Run([]string{"set", "tps", "not-an-integer"}, &stdout, &stderr, nil); got != cliExitUsage {
		t.Fatalf("invalid set value exit = %d, want %d", got, cliExitUsage)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), cliSetUsage) {
		t.Fatalf("stdout/stderr = %q / %q", stdout.String(), stderr.String())
	}
}

func TestRunCLIReportsRemoteProtocolAndTransportErrors(t *testing.T) {
	config := readTestConfigJSON(t)
	malformedServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == api.ConfigPath {
			if _, err := writer.Write(config); err != nil {
				t.Errorf("write config: %v", err)
			}
			return
		}
		_, _ = writer.Write([]byte(`{"unexpected":true}`))
	}))
	defer malformedServer.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve transport test address: %v", err)
	}
	transportURL := "http://" + listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release transport test address: %v", err)
	}

	for _, test := range []struct {
		name       string
		url        string
		wantStderr string
	}{
		{name: "malformed snapshot", url: malformedServer.URL, wantStderr: "decode snapshot"},
		{name: "transport failure", url: transportURL, wantStderr: "GET /api/loadgen/config"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := Run([]string{"status", "--url", test.url}, &stdout, &stderr, nil); got != cliExitRemote {
				t.Fatalf("exit = %d, want %d", got, cliExitRemote)
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), test.wantStderr) {
				t.Fatalf("stdout/stderr = %q / %q", stdout.String(), stderr.String())
			}
		})
	}
}
