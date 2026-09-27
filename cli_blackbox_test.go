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
			name:       "remote help",
			args:       []string{"snapshot", "--help"},
			wantStdout: "usage: ingestion-lab-loadgen snapshot [--url <url>]\n",
			wantExit:   cliExitSuccess,
		},
		{
			name:       "invalid remote arguments",
			args:       []string{"snapshot", "extra"},
			wantStderr: "invalid snapshot arguments\nusage: ingestion-lab-loadgen snapshot [--url <url>]\n",
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

func TestCLIBinaryRemoteSnapshotAndErrors(t *testing.T) {
	binary := buildCLIBinary(t)
	snapshot := testRemoteSnapshot(t)
	expectedSnapshot, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("encode expected snapshot: %v", err)
	}

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

	result := runCLIBinary(t, binary, "snapshot", "--url", snapshotServer.URL)
	if result.exitCode != cliExitSuccess || result.stdout != string(expectedSnapshot)+"\n" || result.stderr != "" {
		t.Fatalf("success stdout/stderr/exit = %q / %q / %d", result.stdout, result.stderr, result.exitCode)
	}
	var actual httpV1Status
	if err := json.Unmarshal([]byte(result.stdout), &actual); err != nil {
		t.Fatalf("decode CLI stdout: %v", err)
	}
	if actual.Run.State != runStatePaused {
		t.Fatalf("snapshot state = %q, want %q", actual.Run.State, runStatePaused)
	}

	httpFailure := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "conflict", http.StatusConflict)
	}))
	defer httpFailure.Close()
	assertCLIBinaryFailure(t, runCLIBinary(t, binary, "snapshot", "--url", httpFailure.URL), cliExitHTTP, "HTTP 409", "")

	malformed := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"unexpected":true}`))
	}))
	defer malformed.Close()
	assertCLIBinaryFailure(t, runCLIBinary(t, binary, "snapshot", "--url", malformed.URL), cliExitRemote, "decode snapshot", "")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve network failure address: %v", err)
	}
	networkURL := "http://" + listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release network failure address: %v", err)
	}
	assertCLIBinaryFailure(t, runCLIBinary(t, binary, "snapshot", "--url", networkURL), cliExitRemote, "GET /api/loadgen/snapshot", "")
}

func TestCLIBinaryServeRunSmoke(t *testing.T) {
	binary := buildCLIBinary(t)
	ensureServePortAvailable(t)

	baseURL := startCLIServe(t, binary)
	waitForCLISnapshotState(t, baseURL, runStateIdle)

	runResult := runCLIBinary(t, binary, "run", "--url", baseURL)
	if runResult.exitCode != cliExitSuccess || runResult.stderr != "" || runResult.stdout != "action=run state=running\n" {
		t.Fatalf("stdout/stderr/exit = %q / %q / %d", runResult.stdout, runResult.stderr, runResult.exitCode)
	}
}

func buildCLIBinary(t *testing.T) string {
	t.Helper()
	buildDirectory := blackBoxRuntimeDirectory(t)
	binary := filepath.Join(buildDirectory, "ingestion-lab-loadgen.exe")
	context, cancel := context.WithTimeout(context.Background(), blackBoxTimeout)
	defer cancel()
	command := exec.CommandContext(context, "go", "build", "-o", binary, ".")
	command.Dir = repositoryRoot(t)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build CLI binary: %v\n%s", err, output)
	}
	return binary
}

func runCLIBinary(t *testing.T, binary string, args ...string) blackBoxResult {
	t.Helper()
	context, cancel := context.WithTimeout(context.Background(), blackBoxTimeout)
	defer cancel()
	command := exec.CommandContext(context, binary, args...)
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

func startCLIServe(t *testing.T, binary string) string {
	t.Helper()
	configPath := writeBlackBoxConfig(t)
	args := []string{"serve", "--config", configPath}
	command := exec.Command(binary, args...)
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

func writeBlackBoxConfig(t *testing.T) string {
	t.Helper()
	directory := blackBoxRuntimeDirectory(t)
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
	configPath := filepath.Join(directory, "config.toml")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write serve config: %v", err)
	}
	return configPath
}

func waitForCLISnapshotState(t *testing.T, baseURL string, want runState) {
	t.Helper()
	deadline := time.Now().Add(blackBoxTimeout)
	client := &http.Client{Timeout: time.Second}
	var lastError error
	for time.Now().Before(deadline) {
		response, err := client.Get(baseURL + snapshotPath)
		if err == nil {
			var snapshot httpV1Status
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

func blackBoxRuntimeDirectory(t *testing.T) string {
	t.Helper()
	repoRoot := repositoryRoot(t)
	runtimeRoot := filepath.Join(filepath.Dir(repoRoot), "ingestion-lab-loadgen-agents-runtime")
	if info, err := os.Stat(runtimeRoot); err != nil || !info.IsDir() {
		t.Fatalf("runtime root %q is unavailable: %v", runtimeRoot, err)
	}
	directory, err := os.MkdirTemp(filepath.Join(runtimeRoot, "BUILD"), "T0447-cli-blackbox-")
	if err != nil {
		t.Fatalf("create runtime build directory: %v", err)
	}
	return directory
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return root
}
