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
	"strings"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
)

const blackBoxTimeout = 10 * time.Second

type blackBoxResult struct {
	stdout   string
	stderr   string
	exitCode int
}

func TestCLIBinaryArgsAndHelp(t *testing.T) {
	binary := buildCLIBinary(t)

	for _, test := range []struct {
		name       string
		args       []string
		wantStdout string
		wantStderr string
		wantExit   int
	}{
		{
			name:       "top-level help",
			args:       []string{"--help"},
			wantStdout: cliUsage + "\n",
			wantExit:   cliExitSuccess,
		},
		{
			name:       "unknown command",
			args:       []string{"inspect"},
			wantStderr: "unexpected command \"inspect\"\n" + cliUsage + "\n",
			wantExit:   cliExitUsage,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := runCLIBinary(t, binary, test.args...)
			if result.exitCode != test.wantExit || result.stdout != test.wantStdout || result.stderr != test.wantStderr {
				t.Fatalf("stdout/stderr/exit = %q / %q / %d", result.stdout, result.stderr, result.exitCode)
			}
		})
	}
}

func TestCLIBinaryRemoteStatusAndErrors(t *testing.T) {
	binary := buildCLIBinary(t)
	snapshot := testRemoteSnapshot(t)

	snapshotServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != snapshotPath {
			http.NotFound(writer, request)
			return
		}
		if err := json.NewEncoder(writer).Encode(snapshot); err != nil {
			t.Fatalf("encode snapshot: %v", err)
		}
	}))
	defer snapshotServer.Close()

	result := runCLIBinary(t, binary, "status", "--url", snapshotServer.URL)
	if result.exitCode != cliExitSuccess || !strings.Contains(result.stdout, "State:                    running") || result.stderr != "" {
		t.Fatalf("success stdout/stderr/exit = %q / %q / %d", result.stdout, result.stderr, result.exitCode)
	}

	httpFailure := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "conflict", http.StatusConflict)
	}))
	defer httpFailure.Close()
	assertCLIBinaryFailure(t, runCLIBinary(t, binary, "status", "--url", httpFailure.URL), cliExitHTTP, "HTTP 409", "")

	malformed := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"unexpected":true}`))
	}))
	defer malformed.Close()
	assertCLIBinaryFailure(t, runCLIBinary(t, binary, "status", "--url", malformed.URL), cliExitRemote, "decode snapshot", "")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve network failure address: %v", err)
	}
	networkURL := "http://" + listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release network failure address: %v", err)
	}
	assertCLIBinaryFailure(t, runCLIBinary(t, binary, "status", "--url", networkURL), cliExitRemote, "GET /api/loadgen/snapshot", "")
}

func TestCLIBinaryServeStartsDeliveryAutomatically(t *testing.T) {
	binary := buildCLIBinary(t)
	ensureServePortAvailable(t)

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
	waitForHTTPSnapshotState(t, baseURL, runStateRunning)

	select {
	case <-received:
	case <-time.After(blackBoxTimeout):
		t.Fatal("Sender did not POST to the separate receiver")
	}
}

func buildCLIBinary(t *testing.T) string {
	t.Helper()
	buildDirectory := t.TempDir()
	binary := filepath.Join(buildDirectory, "ingestion-lab-loadgen.exe")
	ctx, cancel := context.WithTimeout(context.Background(), blackBoxTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build CLI binary: %v\n%s", err, output)
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
	args := []string{"serve", "--config", configPath}
	command := exec.Command(binary, args...)
	command.Dir = filepath.Dir(configPath)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	t.Cleanup(func() {
		if command.ProcessState != nil && command.ProcessState.Exited() {
			return
		}
		if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("stop serve: %v", err)
		}
		if err := command.Wait(); err != nil {
			var exitError *exec.ExitError
			if !errors.As(err, &exitError) {
				t.Errorf("wait for stopped serve: %v", err)
			}
		}
	})
	return defaultCLIBaseURL
}

func writeBlackBoxConfig(t *testing.T, senderURL string) string {
	t.Helper()
	directory := t.TempDir()
	parquetPath := filepath.Join(directory, "input.parquet")
	if err := parquet.WriteFile(parquetPath, []Transaction{{ClientID: "black-box"}}); err != nil {
		t.Fatalf("write parquet fixture: %v", err)
	}
	config := strings.Replace(
		testConfigContents(),
		"C:\\dataset\\*.parquet",
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

func waitForHTTPSnapshotState(t *testing.T, baseURL string, want runState) {
	t.Helper()
	deadline := time.Now().Add(blackBoxTimeout)
	client := &http.Client{Timeout: time.Second}
	var lastError error
	for time.Now().Before(deadline) {
		response, err := client.Get(baseURL + snapshotPath)
		if err == nil {
			var snapshot runtimeSnapshot
			decodeErr := json.NewDecoder(response.Body).Decode(&snapshot)
			closeErr := response.Body.Close()
			if decodeErr == nil && closeErr == nil && snapshot.Run.State == want {
				return
			}
			lastError = fmt.Errorf("snapshot state = %q, want %q; decode=%v; close=%v", snapshot.Run.State, want, decodeErr, closeErr)
		} else {
			lastError = err
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("wait for service state %q: %v", want, lastError)
}

func ensureServePortAvailable(t *testing.T) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:8080")
	if err != nil {
		t.Skipf("serve test requires 127.0.0.1:8080: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("release serve port: %v", err)
	}
}
