package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
		{name: "default snapshot", args: []string{"snapshot"}, wantAction: "snapshot", wantURL: defaultCLIBaseURL},
		{name: "run custom URL", args: []string{"run", "--url", "https://example.test/"}, wantAction: "run", wantURL: "https://example.test"},
		{name: "remote help", args: []string{"pause", "--help"}, wantAction: "pause", wantURL: defaultCLIBaseURL, wantHelp: true},
		{name: "unexpected operand", args: []string{"reset", "now"}, wantErr: true},
		{name: "missing URL", args: []string{"run", "--url"}, wantErr: true},
		{name: "path URL", args: []string{"run", "--url", "http://example.test/base"}, wantErr: true},
		{name: "query URL", args: []string{"run", "--url", "http://example.test/?x=1"}, wantErr: true},
		{name: "userinfo URL", args: []string{"run", "--url", "http://user@example.test"}, wantErr: true},
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
		{name: "reader workers", args: []string{"set", "reader-workers", "3"}, wantTarget: "reader-workers", wantAction: "set-reader-workers", wantValue: 3, wantURL: defaultCLIBaseURL},
		{name: "sender workers custom URL", args: []string{"set", "sender-workers", "4", "--url", "https://example.test/"}, wantTarget: "sender-workers", wantAction: "set-sender-workers", wantValue: 4, wantURL: "https://example.test"},
		{name: "requested TPS", args: []string{"set", "requested-tps", "0"}, wantTarget: "requested-tps", wantAction: "set-requested-tps", wantValue: 0, wantURL: defaultCLIBaseURL},
		{name: "throttler mode", args: []string{"set", "throttler-installed", "false"}, wantTarget: "throttler-installed", wantAction: "set-throttler-installed", wantValue: false, wantURL: defaultCLIBaseURL},
		{name: "throttler true", args: []string{"set", "throttler-installed", "true"}, wantTarget: "throttler-installed", wantAction: "set-throttler-installed", wantValue: true, wantURL: defaultCLIBaseURL},
		{name: "bool alias", args: []string{"set", "throttler-installed", "1"}, wantErr: true},
		{name: "old value", args: []string{"set", "throttler-installed", "bypass"}, wantErr: true},
		{name: "old target", args: []string{"set", "throttler-mode", "false"}, wantErr: true},
		{name: "set help", args: []string{"set", "--help"}, wantHelp: true, wantURL: defaultCLIBaseURL},
		{name: "target help", args: []string{"set", "reader-workers", "--help"}, wantHelp: true, wantURL: defaultCLIBaseURL},
		{name: "unknown target", args: []string{"set", "other", "1"}, wantErr: true},
		{name: "missing value", args: []string{"set", "reader-workers"}, wantErr: true},
		{name: "non integer", args: []string{"set", "requested-tps", "not-an-integer"}, wantErr: true},
		{name: "invalid URL", args: []string{"set", "reader-workers", "2", "--url", "ftp://example.test"}, wantErr: true},
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

func TestParseServeRun(t *testing.T) {
	command, err := parseCLI([]string{"serve", "--config", "custom.toml", "--run"})
	if err != nil {
		t.Fatalf("parseCLI: %v", err)
	}
	if command.configPath != "custom.toml" || !command.runAfterStart {
		t.Fatalf("command = %+v", command)
	}
	if _, err := parseCLI([]string{"serve", "--run", "--run"}); err == nil {
		t.Fatal("duplicate --run accepted")
	}
}

func TestRunRemoteCLIMappingAndOutput(t *testing.T) {
	snapshot := testRemoteSnapshot(t)
	encodedSnapshot, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}

	var requests []struct {
		method string
		path   string
		action string
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		entry := struct {
			method string
			path   string
			action string
		}{method: request.Method, path: request.URL.Path}
		if request.Method == http.MethodPost {
			if got, want := request.Header.Get("Content-Type"), "application/json"; got != want {
				t.Fatalf("Content-Type = %q, want %q", got, want)
			}
			data, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatalf("read command: %v", err)
			}
			if got, want := string(data), `{"action":"run"}`; got != want {
				t.Fatalf("command body = %q, want %q", got, want)
			}
			var body commandRequest
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatalf("decode command: %v", err)
			}
			entry.action = body.Action
		}
		requests = append(requests, entry)
		if request.URL.Path == snapshotPath {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write(encodedSnapshot)
		}
	}))
	defer server.Close()

	var output bytes.Buffer
	if err := runRemoteCLI(context.Background(), cliCommand{remoteAction: "resume", remoteURL: server.URL}, &output); err != nil {
		t.Fatalf("runRemoteCLI: %v", err)
	}
	if got, want := output.String(), "action=resume state=paused\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if len(requests) != 2 || requests[0].method != http.MethodPost || requests[0].path != commandsPath || requests[0].action != "run" || requests[1].method != http.MethodGet || requests[1].path != snapshotPath {
		t.Fatalf("requests = %+v", requests)
	}
}

func TestRunRemoteCLIMapsLifecycleActions(t *testing.T) {
	snapshot := testRemoteSnapshot(t)
	encodedSnapshot, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		action     string
		wantAction string
	}{
		{action: "run", wantAction: "run"},
		{action: "pause", wantAction: "pause"},
		{action: "resume", wantAction: "run"},
		{action: "reset", wantAction: "reset"},
	} {
		t.Run(test.action, func(t *testing.T) {
			var action string
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method == http.MethodPost {
					var body commandRequest
					if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
						t.Fatalf("decode command: %v", err)
					}
					action = body.Action
					return
				}
				_, _ = writer.Write(encodedSnapshot)
			}))
			defer server.Close()

			var output bytes.Buffer
			if err := runRemoteCLI(context.Background(), cliCommand{remoteAction: test.action, remoteURL: server.URL}, &output); err != nil {
				t.Fatalf("runRemoteCLI: %v", err)
			}
			if action != test.wantAction {
				t.Fatalf("request action = %q, want %q", action, test.wantAction)
			}
		})
	}
}

func TestRunRemoteCLIMapsSetActionsAndConfirmsSnapshot(t *testing.T) {
	for _, test := range []struct {
		target        string
		requestAction string
		value         any
		update        func(*runtimeStatus)
	}{
		{target: "reader-workers", requestAction: "set-reader-workers", value: 3, update: func(snapshot *runtimeStatus) { snapshot.Reader.Workers = 3 }},
		{target: "sender-workers", requestAction: "set-sender-workers", value: 4, update: func(snapshot *runtimeStatus) { snapshot.Sender.Workers = 4 }},
		{target: "requested-tps", requestAction: "set-requested-tps", value: 0, update: func(snapshot *runtimeStatus) { snapshot.Throttler.RequestedTps = 0 }},
		{target: "throttler-installed", requestAction: "set-throttler-installed", value: false, update: func(snapshot *runtimeStatus) { snapshot.Throttler.Installed = false }},
		{target: "throttler-installed", requestAction: "set-throttler-installed", value: true, update: func(snapshot *runtimeStatus) { snapshot.Throttler.Installed = true }},
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

			command := cliCommand{remoteAction: "set", remoteURL: server.URL, remoteSet: remoteSetCommand{target: test.target, requestAction: test.requestAction, value: test.value}}
			var output bytes.Buffer
			if err := runRemoteCLI(context.Background(), command, &output); err != nil {
				t.Fatalf("runRemoteCLI: %v", err)
			}
			if requestBody.Action != test.requestAction || string(requestBody.Value) != mustMarshalJSON(t, test.value) {
				t.Fatalf("request = %+v", requestBody)
			}
			wantOutput := "action=set target=" + test.target + " value=" + mustFormatValue(test.value) + " state=paused\n"
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
	err := runRemoteCLI(context.Background(), cliCommand{remoteAction: "set", remoteURL: server.URL, remoteSet: remoteSetCommand{target: "reader-workers", requestAction: "set-reader-workers", value: 3}}, &output)
	if err == nil || !strings.Contains(err.Error(), "verification snapshot reader.workers") {
		t.Fatalf("runRemoteCLI error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", output.String())
	}
}

func TestRunRemoteCLISnapshotStrictlyValidatesSchema(t *testing.T) {
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
	err = runRemoteCLI(context.Background(), cliCommand{remoteAction: "snapshot", remoteURL: server.URL}, &output)
	if err == nil || !strings.Contains(err.Error(), "decode snapshot") {
		t.Fatalf("runRemoteCLI error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", output.String())
	}
}

func TestDecodeStrictSnapshotConfigContract(t *testing.T) {
	snapshot := testRemoteSnapshot(t)
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &root); err != nil {
		t.Fatal(err)
	}
	delete(root, "policy")
	config, err := json.Marshal(snapshot.Config)
	if err != nil {
		t.Fatal(err)
	}
	root["config"] = config
	for _, test := range []struct {
		name    string
		change  func(map[string]json.RawMessage)
		wantErr bool
	}{
		{name: "config accepted", change: func(map[string]json.RawMessage) {}},
		{name: "installed null", wantErr: true, change: func(root map[string]json.RawMessage) {
			root["throttler"] = json.RawMessage(`{"requestedTps":0,"admittedTps":0,"installed":null}`)
		}},
		{name: "installed missing", wantErr: true, change: func(root map[string]json.RawMessage) {
			root["throttler"] = json.RawMessage(`{"requestedTps":0,"admittedTps":0}`)
		}},
		{name: "installed string", wantErr: true, change: func(root map[string]json.RawMessage) {
			root["throttler"] = json.RawMessage(`{"requestedTps":0,"admittedTps":0,"installed":"false"}`)
		}},
		{name: "installed number", wantErr: true, change: func(root map[string]json.RawMessage) {
			root["throttler"] = json.RawMessage(`{"requestedTps":0,"admittedTps":0,"installed":0}`)
		}},
		{name: "only legacy field rejected", wantErr: true, change: func(root map[string]json.RawMessage) { root["policy"] = root["config"]; delete(root, "config") }},
		{name: "both fields rejected", wantErr: true, change: func(root map[string]json.RawMessage) { root["policy"] = root["config"] }},
		{name: "missing config rejected", wantErr: true, change: func(root map[string]json.RawMessage) { delete(root, "config") }},
		{name: "null config rejected", wantErr: true, change: func(root map[string]json.RawMessage) { root["config"] = json.RawMessage(`null`) }},
		{name: "invalid config rejected", wantErr: true, change: func(root map[string]json.RawMessage) { root["config"] = json.RawMessage(`[]`) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := make(map[string]json.RawMessage, len(root))
			for key, value := range root {
				input[key] = value
			}
			test.change(input)
			data, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			got, err := decodeStrictSnapshot(bytes.NewReader(data))
			if test.wantErr {
				if err == nil {
					t.Fatal("invalid snapshot accepted")
				}
				return
			}
			if err != nil {
				t.Fatalf("config snapshot rejected: %v", err)
			}
			if got.Config.ReaderReadBatchSize != snapshot.Config.ReaderReadBatchSize {
				t.Fatalf("decoded config = %+v", got.Config)
			}
		})
	}
}

func TestDecodeStrictSnapshotInitialContract(t *testing.T) {
	encoded, err := json.Marshal(testRemoteSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	fields := []string{
		"readerReadBatchSize", "readerWorkers", "readerChannelCapacity", "senderChannelCapacity",
		"throttlerRequestedTps", "throttlerInstalled", "senderWorkers", "metricsWindowMs",
	}
	for _, field := range fields {
		shapes := []string{"initial-only", "legacy-only", "both", "missing"}
		if field == "throttlerInstalled" {
			shapes = append(shapes, "null", "string", "number", "allowed-null", "allowed-element-null", "allowed-element-string", "allowed-element-number")
		}
		for _, shape := range shapes {
			t.Run(field+"/"+shape, func(t *testing.T) {
				root := map[string]json.RawMessage{}
				if err := json.Unmarshal(encoded, &root); err != nil {
					t.Fatal(err)
				}
				settings := map[string]json.RawMessage{}
				if err := json.Unmarshal(root["config"], &settings); err != nil {
					t.Fatal(err)
				}
				value := map[string]json.RawMessage{}
				if err := json.Unmarshal(settings[field], &value); err != nil {
					t.Fatal(err)
				}
				if _, ok := value["initial"]; !ok {
					t.Fatal("fixture lacks initial")
				}
				if field == "readerChannelCapacity" || field == "senderChannelCapacity" {
					value["initial"] = json.RawMessage(`0`)
				}
				if field == "throttlerInstalled" {
					value["initial"] = json.RawMessage(`false`)
				}
				switch shape {
				case "legacy-only":
					value["default"] = value["initial"]
					delete(value, "initial")
				case "both":
					value["default"] = value["initial"]
				case "missing":
					delete(value, "initial")
				case "null":
					value["initial"] = json.RawMessage(`null`)
				case "string":
					value["initial"] = json.RawMessage(`"false"`)
				case "number":
					value["initial"] = json.RawMessage(`0`)
				case "allowed-null":
					value["allowed"] = json.RawMessage(`null`)
				case "allowed-element-null":
					value["allowed"] = json.RawMessage(`[true,null]`)
				case "allowed-element-string":
					value["allowed"] = json.RawMessage(`[true,"false"]`)
				case "allowed-element-number":
					value["allowed"] = json.RawMessage(`[true,0]`)
				}
				settings[field], err = json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				root["config"], err = json.Marshal(settings)
				if err != nil {
					t.Fatal(err)
				}
				input, err := json.Marshal(root)
				if err != nil {
					t.Fatal(err)
				}
				_, err = decodeStrictSnapshot(bytes.NewReader(input))
				if shape == "initial-only" && err != nil {
					t.Fatalf("initial snapshot rejected: %v", err)
				}
				if shape != "initial-only" && err == nil {
					t.Fatal("legacy or incomplete snapshot accepted")
				}
			})
		}
	}
}

func TestRunCLIExitCodes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := runCLI([]string{"run", "--url", "ftp://example.test"}, &stdout, &stderr); got != cliExitUsage {
		t.Fatalf("invalid URL exit = %d, want %d", got, cliExitUsage)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "conflict", http.StatusConflict)
	}))
	defer server.Close()
	stdout.Reset()
	stderr.Reset()
	if got := runCLI([]string{"run", "--url", server.URL}, &stdout, &stderr); got != cliExitHTTP {
		t.Fatalf("HTTP exit = %d, want %d", got, cliExitHTTP)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "HTTP 409") {
		t.Fatalf("stdout/stderr = %q / %q", stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if got := runCLI([]string{"set", "requested-tps", "not-an-integer"}, &stdout, &stderr); got != cliExitUsage {
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
			if got := runCLI([]string{"snapshot", "--url", test.url}, &stdout, &stderr); got != cliExitRemote {
				t.Fatalf("exit = %d, want %d", got, cliExitRemote)
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), test.wantStderr) {
				t.Fatalf("stdout/stderr = %q / %q", stdout.String(), stderr.String())
			}
		})
	}
}

func testRemoteSnapshot(t *testing.T) runtimeStatus {
	t.Helper()
	return runtimeStatus{
		Run:    runtimeRunStatus{State: runStatePaused},
		Config: runtimeConfigStatusFromConfig(testConfig(t)),
	}
}
