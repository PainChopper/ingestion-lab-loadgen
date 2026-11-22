package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	config := newConfigResponse(testConfig(t))

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

	configPath := writeBlackBoxConfig(t, receiver.URL+"/internal/ingest")
	baseURL := startCLIServe(t, binary, configPath)
	waitForHTTPSnapshotState(t, baseURL, api.RunStateRunning)

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

func startCLIServe(t *testing.T, binary, configPath string) string {
	t.Helper()
	for attempt := range 3 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := listener.Addr().String()
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		loaded, err := config.Load(configPath)
		if err != nil {
			t.Fatal(err)
		}
		contents, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		updated := strings.Replace(string(contents), fmt.Sprintf("addr = %q", loaded.HTTP.Addr), fmt.Sprintf("addr = %q", addr), 1)
		if updated == string(contents) {
			t.Fatal("HTTP address replacement did not apply")
		}
		if err := os.WriteFile(configPath, []byte(updated), 0o600); err != nil {
			t.Fatal(err)
		}
		command := exec.Command(binary, "serve", "--config", configPath)
		command.Dir = filepath.Dir(configPath)
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		if err := command.Start(); err != nil {
			t.Fatalf("start serve: %v", err)
		}
		finished := make(chan struct{})
		var result blackBoxResult
		var waitError error
		go func() {
			waitError = command.Wait()
			result = blackBoxResult{stdout: stdout.String(), stderr: stderr.String()}
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
		baseURL := "http://" + addr
		client := &http.Client{Timeout: 100 * time.Millisecond}
		deadline := time.Now().Add(blackBoxTimeout)
		retry := false
		for time.Now().Before(deadline) {
			select {
			case <-finished:
				if isBindConflict(result, addr) {
					retry = true
					break
				}
				t.Fatalf("serve exited before readiness: wait=%v result=%+v", waitError, result)
			default:
			}
			if retry {
				break
			}
			response, err := client.Get(baseURL + api.SnapshotPath)
			if err == nil {
				var snapshot api.Snapshot
				decodeErr := json.NewDecoder(response.Body).Decode(&snapshot)
				closeErr := response.Body.Close()
				if response.StatusCode == http.StatusOK && decodeErr == nil && closeErr == nil && snapshot.Run.State == api.RunStateRunning {
					ready = true
					t.Logf("serve attempt %d ready at %s", attempt+1, baseURL)
					return baseURL
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !retry {
			stop()
			t.Fatalf("serve readiness timeout: %+v", result)
		}
		t.Logf("retry %d after own subprocess bind conflict at %s", attempt+1, addr)
	}
	t.Fatal("serve exhausted three bind-conflict attempts")
	return ""
}

func isBindConflict(result blackBoxResult, addr string) bool {
	message := syscall.EADDRINUSE.Error()
	if runtime.GOOS == "windows" {
		message = syscall.Errno(10048).Error()
	}
	return result.exitCode == 1 && strings.Contains(result.stderr, "listen HTTP server") &&
		strings.Contains(result.stderr, addr) && strings.Contains(strings.ToLower(result.stderr), strings.ToLower(message))
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
	configPath := writeBlackBoxConfig(t, receiver.URL+"/internal/ingest")
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(contents), "addr = \"127.0.0.1:8080\"", fmt.Sprintf("addr = %q", addr), 1)
	if err := os.WriteFile(configPath, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	result := runCLIBinary(t, binary, "serve", "--config", configPath)
	if !isBindConflict(result, addr) || isBindConflict(result, "127.0.0.1:0") {
		t.Fatalf("real bind conflict misclassified: %+v", result)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	baseURL := startCLIServe(t, binary, configPath)
	waitForHTTPSnapshotState(t, baseURL, api.RunStateRunning)
	t.Logf("real bind conflict at %s detected; ordinary helper restarted at %s", addr, baseURL)
}

func writeBlackBoxConfig(t *testing.T, senderURL string) string {
	t.Helper()
	directory := t.TempDir()
	parquetPath := filepath.Join(directory, "input.parquet")
	if err := parquet.WriteFile(parquetPath, []pipeline.Transaction{{ClientID: "black-box"}}); err != nil {
		t.Fatalf("write parquet fixture: %v", err)
	}
	config := strings.Replace(
		testConfigContents(t),
		"../data/MBD-mini/trx/fold=*/*.parquet",
		filepath.ToSlash(filepath.Join(directory, "*.parquet")),
		1,
	)
	config = strings.Replace(config, testSenderURL, senderURL, 1)
	configPath := filepath.Join(directory, "config.toml")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write serve config: %v", err)
	}
	return configPath
}

func occupyDefaultServePort(t *testing.T) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:8080")
	if err != nil {
		message := syscall.EADDRINUSE.Error()
		if runtime.GOOS == "windows" {
			message = syscall.Errno(10048).Error()
		}
		if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(message)) {
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
	path := writeConfigInput(t, testConfigContents(t))
	t.Chdir(filepath.Dir(path))

	var stdout, stderr bytes.Buffer
	var loaded config.Config
	code := cli.Run(nil, &stdout, &stderr, func(path string) error {
		var err error
		loaded, err = config.Load(path)
		return err
	})
	if code != 0 || loaded.Sender.API.URL != testSenderURL {
		t.Fatalf("default Sender URL = %q, want %q (exit=%d, stderr=%q)", loaded.Sender.API.URL, testSenderURL, code, stderr.String())
	}
}
