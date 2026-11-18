package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
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

func TestRunRemoteCLIMapsSetActionsAndConfirmsSnapshot(t *testing.T) {
	for _, test := range []struct {
		target        string
		requestAction string
		value         any
		update        func(*runtimeSnapshot)
	}{
		{target: "readers", requestAction: "set-reader-workers", value: 3, update: func(snapshot *runtimeSnapshot) { snapshot.Reader.Workers = 3 }},
		{target: "senders", requestAction: "set-sender-workers", value: 4, update: func(snapshot *runtimeSnapshot) { snapshot.Sender.Workers = 4 }},
		{target: "tps", requestAction: "set-requested-tps", value: 0, update: func(snapshot *runtimeSnapshot) { snapshot.Throttler.RequestedTps = 0 }},
		{target: "throttle", requestAction: "set-throttler-installed", value: false, update: func(snapshot *runtimeSnapshot) { snapshot.Throttler.Installed = false }},
		{target: "throttle", requestAction: "set-throttler-installed", value: true, update: func(snapshot *runtimeSnapshot) { snapshot.Throttler.Installed = true }},
	} {
		t.Run(test.target, func(t *testing.T) {
			snapshot := testRemoteSnapshot(t)
			test.update(&snapshot)
			var requestBody commandRequest
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case commandsPath:
					if request.Method != http.MethodPost {
						t.Fatalf("method = %s, want POST", request.Method)
					}
					if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
						t.Fatalf("decode command: %v", err)
					}
				case snapshotPath:
					if err := json.NewEncoder(writer).Encode(snapshot); err != nil {
						t.Fatalf("encode snapshot: %v", err)
					}
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()

			command, err := parseCLI([]string{"set", test.target, mustFormatValue(test.value), "--url", server.URL})
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			if err := runRemoteCLI(context.Background(), command, &output); err != nil {
				t.Fatalf("runRemoteCLI: %v", err)
			}
			if requestBody.Action != test.requestAction || string(requestBody.Value) != mustMarshalJSON(t, test.value) {
				t.Fatalf("request = %+v", requestBody)
			}
			wantOutput := "action=set target=" + test.target + " value=" + mustFormatValue(test.value) + " state=running\n"
			if output.String() != wantOutput {
				t.Fatalf("stdout = %q, want %q", output.String(), wantOutput)
			}
		})
	}
}

func TestRunRemoteCLISetRejectsMismatchedVerificationSnapshot(t *testing.T) {
	snapshot := testRemoteSnapshot(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == snapshotPath {
			_ = json.NewEncoder(writer).Encode(snapshot)
		}
	}))
	defer server.Close()

	var output bytes.Buffer
	command, err := parseCLI([]string{"set", "readers", "3", "--url", server.URL})
	if err != nil {
		t.Fatal(err)
	}
	err = runRemoteCLI(context.Background(), command, &output)
	if err == nil || !strings.Contains(err.Error(), "verification snapshot reader.workers") {
		t.Fatalf("runRemoteCLI error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", output.String())
	}
}

func TestRunCLISetRequiresExplicitTargetValue(t *testing.T) {
	for _, command := range []struct {
		target, value, section, field string
		valid, wrongType, mismatch    json.RawMessage
	}{
		{target: "readers", value: "3", section: "reader", field: "workers", valid: json.RawMessage(`3`), wrongType: json.RawMessage(`true`), mismatch: json.RawMessage(`4`)},
		{target: "senders", value: "4", section: "sender", field: "workers", valid: json.RawMessage(`4`), wrongType: json.RawMessage(`false`), mismatch: json.RawMessage(`5`)},
		{target: "tps", value: "0", section: "throttler", field: "requestedTps", valid: json.RawMessage(`0`), wrongType: json.RawMessage(`false`), mismatch: json.RawMessage(`1`)},
		{target: "throttle", value: "false", section: "throttler", field: "installed", valid: json.RawMessage(`false`), wrongType: json.RawMessage(`0`), mismatch: json.RawMessage(`true`)},
	} {
		t.Run(command.target, func(t *testing.T) {
			for _, shape := range []string{"explicit", "only target", "missing", "missing section", "null", "string", "wrong type", "mismatch"} {
				t.Run(shape, func(t *testing.T) {
					root := map[string]json.RawMessage{}
					if err := json.Unmarshal([]byte(mustMarshalJSON(t, testRemoteSnapshot(t))), &root); err != nil {
						t.Fatal(err)
					}
					section := map[string]json.RawMessage{}
					if err := json.Unmarshal(root[command.section], &section); err != nil {
						t.Fatal(err)
					}
					section[command.field] = command.valid
					switch shape {
					case "missing":
						delete(section, command.field)
					case "null":
						section[command.field] = json.RawMessage(`null`)
					case "string":
						section[command.field] = json.RawMessage(mustMarshalJSON(t, command.value))
					case "wrong type":
						section[command.field] = command.wrongType
					case "mismatch":
						section[command.field] = command.mismatch
					}
					root[command.section] = json.RawMessage(mustMarshalJSON(t, section))
					if shape == "missing section" {
						delete(root, command.section)
					}
					if shape == "only target" {
						root = map[string]json.RawMessage{
							"run":           json.RawMessage(`{"state":"running"}`),
							command.section: json.RawMessage(mustMarshalJSON(t, map[string]json.RawMessage{command.field: command.valid})),
						}
					}
					body := mustMarshalJSON(t, root)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == snapshotPath {
							_, _ = w.Write([]byte(body))
						}
					}))
					defer server.Close()
					var stdout, stderr bytes.Buffer
					code := runCLI([]string{"set", command.target, command.value, "--url", server.URL}, &stdout, &stderr)
					if shape == "explicit" || shape == "only target" {
						if code != 0 || !strings.Contains(stdout.String(), "target="+command.target+" value="+command.value) || stderr.Len() != 0 {
							t.Fatalf("explicit value: exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
						}
					} else if code != cliExitRemote || stdout.Len() != 0 || stderr.Len() == 0 {
						t.Fatalf("invalid target field: exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
					}
				})
			}
		})
	}
}

func TestRunRemoteCLIStatusStrictlyValidatesSchema(t *testing.T) {
	snapshot := testRemoteSnapshot(t)
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &root); err != nil {
		t.Fatal(err)
	}
	root["unexpected"] = json.RawMessage(`true`)
	invalid, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write(invalid)
	}))
	defer server.Close()

	var output bytes.Buffer
	err = runRemoteCLI(context.Background(), cliCommand{remoteAction: "status", remoteURL: server.URL}, &output)
	if err == nil || !strings.Contains(err.Error(), "decode snapshot") {
		t.Fatalf("runRemoteCLI error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", output.String())
	}
}

func TestDecodeStrictSnapshotValidatesJSONAndState(t *testing.T) {
	for _, test := range []struct {
		name, body string
		wantErr    bool
	}{
		{name: "partial status", body: `{"run":{"state":"running"}}`},
		{name: "starting", body: `{"run":{"state":"starting"}}`},
		{name: "stopping", body: `{"run":{"state":"stopping"}}`},
		{name: "faulted", body: `{"run":{"state":"faulted"}}`},
		{name: "missing and null unrelated values", body: `{"run":{"state":"running"},"reader":null,"throttler":{"requestedTps":null,"installed":null}}`},
		{name: "missing state", body: `{"run":{}}`, wantErr: true},
		{name: "null state", body: `{"run":{"state":null}}`, wantErr: true},
		{name: "invalid state", body: `{"run":{"state":"invalid"}}`, wantErr: true},
		{name: "unknown root field", body: `{"run":{"state":"running"},"unexpected":true}`, wantErr: true},
		{name: "unknown nested field", body: `{"run":{"state":"running"},"reader":{"unexpected":true}}`, wantErr: true},
		{name: "wrong known type", body: `{"run":{"state":"running"},"throttler":{"requestedTps":"0"}}`, wantErr: true},
		{name: "array root", body: `[]`, wantErr: true},
		{name: "null root", body: `null`, wantErr: true},
		{name: "two objects", body: `{"run":{"state":"running"}} {}`, wantErr: true},
		{name: "trailing null", body: `{"run":{"state":"running"}} null`, wantErr: true},
		{name: "trailing garbage", body: `{"run":{"state":"running"}} broken`, wantErr: true},
		{name: "malformed JSON", body: `{"run":`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeStrictSnapshot(strings.NewReader(test.body))
			if (err != nil) != test.wantErr {
				t.Fatalf("decode error=%v, wantErr=%v", err, test.wantErr)
			}
		})
	}
}

func TestRunCLIExitCodes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := runCLI([]string{"status", "--url", "ftp://example.test"}, &stdout, &stderr); got != cliExitUsage {
		t.Fatalf("invalid URL exit = %d, want %d", got, cliExitUsage)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "conflict", http.StatusConflict)
	}))
	defer server.Close()
	stdout.Reset()
	stderr.Reset()
	if got := runCLI([]string{"set", "tps", "0", "--url", server.URL}, &stdout, &stderr); got != cliExitHTTP {
		t.Fatalf("HTTP exit = %d, want %d", got, cliExitHTTP)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "HTTP 409") {
		t.Fatalf("stdout/stderr = %q / %q", stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if got := runCLI([]string{"set", "tps", "not-an-integer"}, &stdout, &stderr); got != cliExitUsage {
		t.Fatalf("invalid set value exit = %d, want %d", got, cliExitUsage)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), cliSetUsage) {
		t.Fatalf("stdout/stderr = %q / %q", stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if got := runCLI([]string{"serve", "--config", "missing-config.toml"}, &stdout, &stderr); got != cliExitLocal {
		t.Fatalf("local startup exit = %d, want %d", got, cliExitLocal)
	}
}

func mustMarshalJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %v: %v", value, err)
	}
	return string(encoded)
}

func mustFormatValue(value any) string {
	return fmt.Sprint(value)
}

func TestRunCLIReportsRemoteProtocolAndTransportErrors(t *testing.T) {
	malformedServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
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
		{name: "transport failure", url: transportURL, wantStderr: "GET /api/loadgen/snapshot"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := runCLI([]string{"status", "--url", test.url}, &stdout, &stderr); got != cliExitRemote {
				t.Fatalf("exit = %d, want %d", got, cliExitRemote)
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), test.wantStderr) {
				t.Fatalf("stdout/stderr = %q / %q", stdout.String(), stderr.String())
			}
		})
	}
}

func testRemoteSnapshot(t *testing.T) runtimeSnapshot {
	t.Helper()
	return runtimeSnapshot{
		Run:    runSnapshot{State: runStateRunning},
		Config: snapshotConfig(testConfig(t)),
	}
}
