package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"
)

func TestRunRoot(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want cliResult
	}{
		{name: "help command", args: []string{"help"}, want: cliResult{stdout: "usage:", usage: "<command>"}},
		{name: "help flag", args: []string{"--help"}, want: cliResult{stdout: "usage:", usage: "<command>"}},
		{name: "short help flag", args: []string{"-h"}, want: cliResult{stdout: "usage:", usage: "<command>"}},
		{name: "unknown command", args: []string{"inspect"}, want: cliResult{code: 2, stderr: "unexpected command", usage: "<command>"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			checkCLIResult(t, test.args, test.want, nil)
		})
	}
}

func TestRunServe(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		path string
		want cliResult
	}{
		{name: "no command starts with default config"},
		{name: "serve starts with default config", args: []string{"serve"}},
		{name: "documented config option", args: []string{"serve", "--config", "custom.toml"}, path: "custom.toml"},
		{name: "single dash with separate value", args: []string{"serve", "-config", "custom.toml"}, path: "custom.toml"},
		{name: "single dash with attached value", args: []string{"serve", "-config=custom.toml"}, path: "custom.toml"},
		{name: "last config wins and accepts a leading dash", args: []string{"serve", "-config=first.toml", "--config", "-private.toml", "--"}, path: "-private.toml"},
		{name: "help after config", args: []string{"serve", "-config=custom.toml", "-h"}, want: cliResult{stdout: "usage:", usage: "<command>"}},
		{name: "config parameter required", args: []string{"serve", "--config"}, want: cliResult{code: 2, stderr: "needs an argument", usage: "<command>"}},
		{name: "invalid flag syntax", args: []string{"serve", "---config"}, want: cliResult{code: 2, stderr: "flag syntax", usage: "<command>"}},
		{name: "empty explicit config", args: []string{"serve", "--config="}, want: cliResult{code: 2, stderr: "non-empty path", usage: "<command>"}},
		{name: "surplus position", args: []string{"serve", "--", "extra"}, want: cliResult{code: 2, stderr: "unexpected argument", usage: "<command>"}},
		{name: "local startup fails", args: []string{"serve"}, want: cliResult{code: 1, stderr: "cannot start service"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			serve := func(path string) error {
				called = true
				if path != test.path {
					t.Errorf("config path = %q, want %q", path, test.path)
				}
				if test.want.code == 1 {
					return errors.New("cannot start service")
				}
				return nil
			}
			checkCLIResult(t, test.args, test.want, serve)
			wantCalled := test.want.usage == ""
			if called != wantCalled {
				t.Fatalf("serve called = %t, want %t", called, wantCalled)
			}
		})
	}
}

func TestRunStatus(t *testing.T) {
	const usage = "status [--url <url>]"
	config := readTestConfigJSON(t)
	for _, test := range []struct {
		name        string
		args        []string
		want        cliResult
		wantBaseURL string
		httpStatus  int
		badSnapshot bool
	}{
		{name: "default endpoint", wantBaseURL: "http://127.0.0.1:8080"},
		{name: "HTTPS endpoint without trailing slash", args: []string{"--url", "https://example.test/"}, wantBaseURL: "https://example.test"},
		{name: "last URL wins and accepts a trailing slash", args: []string{"--url", "invalid", "-url={url}/"}, httpStatus: 200, want: cliResult{stdout: "INGESTION LAB LOADGEN STATUS"}},
		{name: "help flag", args: []string{"--help"}, want: cliResult{stdout: "usage:", usage: usage}},
		{name: "unknown flag", args: []string{"--watch"}, want: cliResult{code: 2, stderr: "not defined", usage: usage}},
		{name: "missing URL parameter", args: []string{"--url"}, want: cliResult{code: 2, stderr: "needs an argument", usage: usage}},
		{name: "empty explicit URL", args: []string{"--url="}, want: cliResult{code: 2, stderr: "invalid status arguments", usage: usage}},
		{name: "surplus position", args: []string{"--", "extra"}, want: cliResult{code: 2, stderr: "invalid status arguments", usage: usage}},
		{name: "relative URL", args: []string{"--url=example.test"}, want: cliResult{code: 2, stderr: "absolute HTTP(S) URL", usage: usage}},
		{name: "malformed URL", args: []string{"--url=http://example.test/%invalid"}, want: cliResult{code: 2, stderr: "absolute HTTP(S) URL", usage: usage}},
		{name: "URL without host", args: []string{"--url=http:///"}, want: cliResult{code: 2, stderr: "URL with host", usage: usage}},
		{name: "unsupported URL scheme", args: []string{"--url=ftp://example.test"}, want: cliResult{code: 2, stderr: "must use http or https", usage: usage}},
		{name: "fragment is disallowed", args: []string{"--url=http://example.test/#x"}, want: cliResult{code: 2, stderr: "must not include", usage: usage}},
		{name: "base path is disallowed", args: []string{"--url=http://example.test/base"}, want: cliResult{code: 2, stderr: "must not include", usage: usage}},
		{name: "query is disallowed", args: []string{"--url=http://example.test/?x=1"}, want: cliResult{code: 2, stderr: "must not include", usage: usage}},
		{name: "userinfo is disallowed", args: []string{"--url=http://user@example.test"}, want: cliResult{code: 2, stderr: "must not include", usage: usage}},
		{name: "HTTP failure", args: []string{"--url={url}"}, httpStatus: 409, want: cliResult{code: 3, stderr: "HTTP 409"}},
		{name: "malformed snapshot", args: []string{"--url={url}"}, httpStatus: 200, badSnapshot: true, want: cliResult{code: 4, stderr: "decode snapshot"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"status"}, test.args...)
			if test.wantBaseURL != "" {
				// Verify the selected address without connecting to an external or fixed local port.
				assertCLIBaseURL(t, args, test.wantBaseURL)
				return
			}
			if test.httpStatus != 0 {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if test.httpStatus != http.StatusOK {
						w.WriteHeader(test.httpStatus)
						return
					}
					switch r.URL.Path {
					case api.ConfigPath:
						_, _ = w.Write(config)
					case api.SnapshotPath:
						if test.badSnapshot {
							_, _ = io.WriteString(w, `{"unexpected":true}`)
							return
						}
						if err := json.NewEncoder(w).Encode(testRemoteSnapshot()); err != nil {
							t.Errorf("encode snapshot: %v", err)
						}
					default:
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				fillCLIURL(args, server.URL)
			}
			checkCLIResult(t, args, test.want, nil)
		})
	}
}

func TestRunSet(t *testing.T) {
	const usage = "set readers <N>"
	for _, test := range []struct {
		name           string
		args           []string
		want           cliResult
		wantBaseURL    string
		wantAction     string
		wantValue      any
		updateSnapshot func(*api.Snapshot)
	}{
		{name: "default endpoint", args: []string{"readers", "3"}, wantBaseURL: "http://127.0.0.1:8080"},
		{name: "reader workers", args: []string{"readers", "3", "--url", "{url}"}, wantAction: "set-reader-workers", wantValue: 3, updateSnapshot: func(s *api.Snapshot) { s.Reader.Workers = 3 }},
		{name: "signed sender workers and last tail URL", args: []string{"senders", "-4", "--url=invalid", "-url={url}/", "--"}, wantAction: "set-sender-workers", wantValue: -4, updateSnapshot: func(s *api.Snapshot) { s.Sender.Workers = -4 }},
		{name: "zero TPS", args: []string{"tps", "0", "--url", "{url}"}, wantAction: "set-requested-tps", wantValue: 0, updateSnapshot: func(s *api.Snapshot) { s.Throttler.RequestedTps = 0 }},
		{name: "positive TPS", args: []string{"tps", "+2000001", "--url", "{url}"}, wantAction: "set-requested-tps", wantValue: 2000001, updateSnapshot: func(s *api.Snapshot) { s.Throttler.RequestedTps = 2000001 }},
		{name: "enable throttle", args: []string{"throttle", "true", "--url", "{url}"}, wantAction: "set-throttler-installed", wantValue: true, updateSnapshot: func(s *api.Snapshot) { s.Throttler.Installed = true }},
		{name: "disable throttle", args: []string{"throttle", "false", "--url", "{url}"}, wantAction: "set-throttler-installed", wantValue: false, updateSnapshot: func(s *api.Snapshot) { s.Throttler.Installed = false }},
		{name: "command help", args: []string{"--help"}, want: cliResult{stdout: "usage:", usage: usage}},
		{name: "target help", args: []string{"readers", "-h"}, want: cliResult{stdout: "usage:", usage: usage}},
		{name: "help following value", args: []string{"tps", "1", "--help"}, want: cliResult{stdout: "usage:", usage: usage}},
		{name: "missing target", want: cliResult{code: 2, stderr: "invalid set arguments", usage: usage}},
		{name: "unknown target", args: []string{"other", "1"}, want: cliResult{code: 2, stderr: "unknown set target", usage: usage}},
		{name: "missing value", args: []string{"readers"}, want: cliResult{code: 2, stderr: "invalid set arguments", usage: usage}},
		{name: "empty explicit value", args: []string{"readers", ""}, want: cliResult{code: 2, stderr: "invalid set arguments", usage: usage}},
		{name: "TPS requires an integer", args: []string{"tps", "1.5"}, want: cliResult{code: 2, stderr: "invalid tps value", usage: usage}},
		{name: "throttle rejects numeric boolean aliases", args: []string{"throttle", "1"}, want: cliResult{code: 2, stderr: "invalid throttle value", usage: usage}},
		{name: "unsupported tail flag", args: []string{"readers", "3", "--other"}, want: cliResult{code: 2, stderr: "not defined", usage: usage}},
		{name: "missing tail flag parameter", args: []string{"readers", "3", "--url"}, want: cliResult{code: 2, stderr: "needs an argument", usage: usage}},
		{name: "empty final URL", args: []string{"readers", "3", "--url=http://example.test", "--url="}, want: cliResult{code: 2, stderr: "invalid set arguments", usage: usage}},
		{name: "surplus position", args: []string{"readers", "3", "extra"}, want: cliResult{code: 2, stderr: "invalid set arguments", usage: usage}},
		{name: "final URL validated before value", args: []string{"tps", "bad", "--url=http://example.test", "--url=ftp://example.test"}, want: cliResult{code: 2, stderr: "must use http or https", usage: usage}},
		{name: "HTTP failure", args: []string{"tps", "0", "--url", "{url}"}, want: cliResult{code: 3, stderr: "HTTP 409"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"set"}, test.args...)
			switch {
			case test.wantBaseURL != "":
				assertCLIBaseURL(t, args, test.wantBaseURL)
			case test.wantAction != "":
				snapshot := testRemoteSnapshot()
				test.updateSnapshot(&snapshot)
				server, checkRequests := setCommandServer(t, snapshot, test.wantAction, test.wantValue)
				defer server.Close()
				fillCLIURL(args, server.URL)
				want := fmt.Sprintf("action=set target=%s value=%v state=running\n", args[1], test.wantValue)
				if stdout := checkCLIResult(t, args, cliResult{stdout: want}, nil); stdout != want {
					t.Errorf("stdout = %q, want %q", stdout, want)
				}
				checkRequests()
			case test.want.code == 3:
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "conflict", http.StatusConflict)
				}))
				defer server.Close()
				fillCLIURL(args, server.URL)
				checkCLIResult(t, args, test.want, nil)
			default:
				checkCLIResult(t, args, test.want, nil)
			}
		})
	}
}

type cliResult struct {
	code   int
	stdout string
	stderr string
	usage  string
}

func checkCLIResult(t *testing.T, args []string, want cliResult, serve func(string) error) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	got := Run(args, &stdout, &stderr, serve)
	if got != want.code {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", got, want.code, stdout.String(), stderr.String())
	}
	for _, stream := range []struct {
		name, got, want string
	}{
		{name: "stdout", got: stdout.String(), want: want.stdout},
		{name: "stderr", got: stderr.String(), want: want.stderr},
	} {
		if stream.want == "" && stream.got != "" || !strings.Contains(stream.got, stream.want) {
			t.Errorf("%s = %q, want content %q", stream.name, stream.got, stream.want)
		}
	}
	wantUsageCount := 0
	if want.usage != "" {
		wantUsageCount = 1
		output := stdout.String()
		if want.code == 2 {
			output = stderr.String()
		}
		if !strings.Contains(output, want.usage) {
			t.Errorf("usage missing %q (stdout=%q stderr=%q)", want.usage, stdout.String(), stderr.String())
		}
	}
	if count := strings.Count(stdout.String(), "usage:") + strings.Count(stderr.String(), "usage:"); count != wantUsageCount {
		t.Errorf("usage count = %d, want %d (stdout=%q stderr=%q)", count, wantUsageCount, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func assertCLIBaseURL(t *testing.T, args []string, want string) {
	t.Helper()
	cmd, err := parseCommand(args, io.Discard)
	if err != nil || cmd.remoteURL != want {
		t.Fatalf("base URL = %q, error = %v, want %q", cmd.remoteURL, err, want)
	}
}

func fillCLIURL(args []string, url string) {
	for index, arg := range args {
		args[index] = strings.ReplaceAll(arg, "{url}", url)
	}
}

func setCommandServer(t *testing.T, snapshot api.Snapshot, action string, value any) (*httptest.Server, func()) {
	t.Helper()
	wantJSON := mustMarshalJSON(t, value)
	var requests []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case api.CommandsPath:
			request, err := api.DecodeCommand(r.Body)
			if err != nil || request.Action != action || string(request.Value) != wantJSON {
				t.Errorf("command request = %+v, error = %v, want action %q and JSON %s", request, err, action, wantJSON)
			}
		case api.SnapshotPath:
			if err := json.NewEncoder(w).Encode(snapshot); err != nil {
				t.Errorf("encode snapshot: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	return server, func() {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		want := []string{"POST " + api.CommandsPath, "GET " + api.SnapshotPath}
		if !slices.Equal(requests, want) {
			t.Errorf("requests = %v, want %v", requests, want)
		}
	}
}
