package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"
	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/cli"
	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/config"
	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/pipeline"

	"github.com/parquet-go/parquet-go"
)

type blackBoxResult struct {
	stdout   string
	stderr   string
	exitCode int
}

// logfmtFields keeps quoted values together, including escaped quotes.
var logfmtFields = regexp.MustCompile(`(?:[^\s"]+|"(?:[^"\\]|\\.)*")+`)

type eventLog struct {
	output  bytes.Buffer
	scanned int
	events  map[string]chan struct{}
}

func newEventLog(names ...string) *eventLog {
	log := &eventLog{events: make(map[string]chan struct{}, len(names))}
	for _, name := range names {
		log.events[name] = make(chan struct{})
	}
	return log
}

func (log *eventLog) Write(data []byte) (int, error) {
	n, _ := log.output.Write(data)
	pending := log.output.Bytes()[log.scanned:]
	for {
		line, rest, found := bytes.Cut(pending, []byte{'\n'})
		if !found {
			break
		}
		pending = rest
		log.scanned += len(line) + 1
		fields := logfmtFields.FindAllString(string(line), -1)
		for name, ready := range log.events {
			if slices.Contains(fields, "event="+name) {
				select {
				case <-ready:
				default:
					close(ready)
				}
			}
		}
	}
	return n, nil
}

func TestCLIBinaryArgsAndHelp(t *testing.T) {
	binary := buildCLIBinary(t)

	for _, test := range []struct {
		name            string
		args            []string
		wantErrorPrefix string
		wantExit        int
	}{
		{
			name:     "top-level help",
			args:     []string{"--help"},
			wantExit: 0,
		},
		{
			name:            "unknown command",
			args:            []string{"inspect"},
			wantErrorPrefix: "unexpected command \"inspect\"\n",
			wantExit:        2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := cli.Run(test.args, &stdout, &stderr, nil); code != test.wantExit {
				t.Fatalf("CLI package exit = %d, want %d", code, test.wantExit)
			}
			if test.wantErrorPrefix != "" && !strings.HasPrefix(stderr.String(), test.wantErrorPrefix) {
				t.Fatalf("stderr = %q, want prefix %q", stderr.String(), test.wantErrorPrefix)
			}
			result := runCLIBinary(t, binary, test.args...)
			if result.exitCode != test.wantExit || result.stdout != stdout.String() || result.stderr != stderr.String() {
				t.Fatalf("stdout/stderr/exit = %q / %q / %d", result.stdout, result.stderr, result.exitCode)
			}
		})
	}
}

func TestCLIBinaryRemoteStatusAndErrors(t *testing.T) {
	binary := buildCLIBinary(t)
	data, err := os.ReadFile("testdata/snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := api.DecodeSnapshot(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Run.State = api.RunStateRunning
	config := newConfigResponse(testConfig())

	snapshotServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			http.NotFound(writer, request)
			return
		}
		if request.URL.Path == api.ConfigPath {
			if err := json.NewEncoder(writer).Encode(config); err != nil {
				t.Errorf("encode config: %v", err)
			}
			return
		}
		if request.URL.Path != api.SnapshotPath {
			http.NotFound(writer, request)
			return
		}
		if err := json.NewEncoder(writer).Encode(snapshot); err != nil {
			t.Errorf("encode snapshot: %v", err)
		}
	}))
	defer snapshotServer.Close()

	t.Run("success", func(t *testing.T) {
		result := runCLIBinary(t, binary, "status", "--url", snapshotServer.URL)
		if result.exitCode != 0 || !strings.Contains(result.stdout, "State:                    running") || result.stderr != "" {
			t.Fatalf("success stdout/stderr/exit = %q / %q / %d", result.stdout, result.stderr, result.exitCode)
		}
	})

	httpFailure := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "conflict", http.StatusConflict)
	}))
	defer httpFailure.Close()
	t.Run("HTTP conflict", func(t *testing.T) {
		assertCLIBinaryFailure(t, runCLIBinary(t, binary, "status", "--url", httpFailure.URL), 3, "HTTP 409", "")
	})

	malformed := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"unexpected":true}`))
	}))
	defer malformed.Close()
	t.Run("invalid schema", func(t *testing.T) {
		assertCLIBinaryFailure(t, runCLIBinary(t, binary, "status", "--url", malformed.URL), 4, "GET "+api.ConfigPath+": decode config", "")
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve network failure address: %v", err)
	}
	networkURL := "http://" + listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release network failure address: %v", err)
	}
	t.Run("network failure", func(t *testing.T) {
		assertCLIBinaryFailure(t, runCLIBinary(t, binary, "status", "--url", networkURL), 4, "GET "+api.ConfigPath, "")
	})
}

func TestCLIBinaryServeStartsDeliveryAutomatically(t *testing.T) {
	binary := buildCLIBinary(t)
	occupyDefaultServePort(t)

	received := make(chan struct{}, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/internal/ingest" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		select {
		case received <- struct{}{}:
		default:
		}
	}))
	defer receiver.Close()

	cfg := blackBoxConfig(t, receiver.URL+"/internal/ingest")
	startCLIServe(t, binary, cfg)

	select {
	case <-received:
	case <-time.After(blackBoxTimeout):
		t.Fatal("Sender did not POST to the separate receiver")
	}
}

func buildCLIBinary(t *testing.T) string {
	t.Helper()
	const buildTimeout = 2 * time.Minute
	buildDirectory := t.TempDir()
	binary := filepath.Join(buildDirectory, "ingestion-lab-loadgen.exe")
	ctx, cancel := context.WithTimeout(t.Context(), buildTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build CLI binary: %v\n%s", errors.Join(err, ctx.Err()), output)
	}
	return binary
}

func runCLIBinary(t *testing.T, binary string, args ...string) blackBoxResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), blackBoxTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result := blackBoxResult{stdout: stdout.String(), stderr: stderr.String()}
	if err == nil {
		return result
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("run CLI %q: %v", args, err)
	}
	result.exitCode = exitError.ExitCode()
	return result
}

func assertCLIBinaryFailure(t *testing.T, result blackBoxResult, wantExit int, wantStderr, wantStdout string) {
	t.Helper()
	if result.exitCode != wantExit || result.stdout != wantStdout || !strings.Contains(result.stderr, wantStderr) {
		t.Fatalf("stdout/stderr/exit = %q / %q / %d", result.stdout, result.stderr, result.exitCode)
	}
}

func startCLIServe(t *testing.T, binary string, cfg config.Config) string {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.toml")
	for attempt := range 3 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := listener.Addr().String()
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		cfg.HTTP.Addr = addr
		writeTestConfig(t, configPath, cfg)
		command := exec.Command(binary, "serve", "--config", configPath)
		command.Dir = filepath.Dir(configPath)
		stdout := newEventLog("run_started")
		var stderr bytes.Buffer
		command.Stdout, command.Stderr = stdout, &stderr
		if err := command.Start(); err != nil {
			t.Fatalf("start serve: %v", err)
		}
		finished := make(chan struct{})
		var result blackBoxResult
		var waitError error
		go func() {
			waitError = command.Wait()
			result = blackBoxResult{stdout: stdout.output.String(), stderr: stderr.String()}
			var exitError *exec.ExitError
			if errors.As(waitError, &exitError) {
				result.exitCode = exitError.ExitCode()
			}
			close(finished)
		}()
		ready := false
		stop := func() {
			select {
			case <-finished:
				return
			default:
			}
			if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Errorf("stop own serve: %v", err)
			}
			<-finished
		}
		t.Cleanup(func() {
			stop()
			if ready && (strings.Count(result.stdout, "event=service_started") != 1 || !strings.Contains(result.stdout, "http_addr="+addr)) {
				t.Errorf("service start log must appear once with actual listener %s: %s", addr, result.stdout)
			}
		})
		select {
		case <-stdout.events["run_started"]:
		case <-finished:
			if isBindConflict(result, addr) {
				t.Logf("retry %d after own subprocess bind conflict at %s", attempt+1, addr)
				continue
			}
			t.Fatalf("serve exited before readiness: wait=%v result=%+v", waitError, result)
		case <-time.After(blackBoxTimeout):
			stop()
			t.Fatalf("serve readiness timeout waiting for run_started: %+v", result)
		}
		baseURL := "http://" + addr
		client := &http.Client{Timeout: blackBoxTimeout}
		response, err := client.Get(baseURL + api.SnapshotPath)
		if err != nil {
			stop()
			t.Fatalf("GET snapshot after run_started: %v, result=%+v", err, result)
		}
		var snapshot api.Snapshot
		decodeErr := json.NewDecoder(response.Body).Decode(&snapshot)
		closeErr := response.Body.Close()
		if response.StatusCode != http.StatusOK || decodeErr != nil || closeErr != nil || snapshot.Run.State != api.RunStateRunning {
			stop()
			t.Fatalf("snapshot after run_started: HTTP %d, state=%q, decode=%v, close=%v, result=%+v",
				response.StatusCode, snapshot.Run.State, decodeErr, closeErr, result)
		}
		ready = true
		t.Logf("serve attempt %d ready at %s", attempt+1, baseURL)
		return baseURL
	}
	t.Fatal("serve exhausted three bind-conflict attempts")
	return ""
}

func isBindConflict(result blackBoxResult, addr string) bool {
	return result.exitCode == 1 && strings.Contains(result.stderr, "listen HTTP server") &&
		strings.Contains(result.stderr, addr) &&
		strings.Contains(strings.ToLower(result.stderr), strings.ToLower(addressInUseError().Error()))
}

func addressInUseError() syscall.Errno {
	if runtime.GOOS == "windows" {
		return syscall.Errno(10048)
	}
	return syscall.EADDRINUSE
}

func TestCLIBinaryServeReportsRealBindConflict(t *testing.T) {
	binary := buildCLIBinary(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	addr := listener.Addr().String()
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(receiver.Close)
	cfg := blackBoxConfig(t, receiver.URL+"/internal/ingest")
	cfg.HTTP.Addr = addr
	configPath := filepath.Join(t.TempDir(), "config.toml")
	writeTestConfig(t, configPath, cfg)
	result := runCLIBinary(t, binary, "serve", "--config", configPath)
	if !isBindConflict(result, addr) || isBindConflict(result, "127.0.0.1:0") {
		t.Fatalf("real bind conflict misclassified: %+v", result)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	baseURL := startCLIServe(t, binary, cfg)
	t.Logf("real bind conflict at %s detected; ordinary helper restarted at %s", addr, baseURL)
}

func blackBoxConfig(t *testing.T, senderURL string) config.Config {
	t.Helper()
	cfg := testConfig()
	cfg.Source.Path = filepath.Join(t.TempDir(), "input.parquet")
	cfg.Sender.API.URL = senderURL
	if err := parquet.WriteFile(cfg.Source.Path, []pipeline.Transaction{{ClientID: "black-box"}}); err != nil {
		t.Fatalf("write parquet fixture: %v", err)
	}
	return cfg
}

func occupyDefaultServePort(t *testing.T) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:8080")
	if err != nil {
		if !errors.Is(err, addressInUseError()) {
			t.Fatalf("reserve default port: %v", err)
		}
		t.Logf("default 8080 already occupied; existing listener left untouched: %v", err)
		return
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	})
	t.Log("own test listener holds 8080 throughout serve startup and delivery")
}

func TestRunCLIReportsLocalStartupFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{"serve", "--config", filepath.Join(t.TempDir(), "missing-config.toml")}
	if got := cli.Run(args, &stdout, &stderr, serveWithSignals); got != 1 {
		t.Fatalf("local startup exit = %d, want 1", got)
	}
}

func TestDefaultServeUsesCurrentDefaultConfig(t *testing.T) {
	cfg := testConfig()
	cfg.Sender.API.URL = "http://127.0.0.1:50001/ingest"
	t.Chdir(t.TempDir())
	writeTestConfig(t, "config.toml", cfg)

	var stdout, stderr bytes.Buffer
	var loaded config.Config
	code := cli.Run(nil, &stdout, &stderr, func(path string) error {
		var err error
		loaded, err = config.Load(path)
		return err
	})
	if code != 0 || loaded.Sender.API.URL != cfg.Sender.API.URL {
		t.Fatalf("default Sender URL = %q, want %q (exit=%d, stderr=%q)", loaded.Sender.API.URL, cfg.Sender.API.URL, code, stderr.String())
	}
}
