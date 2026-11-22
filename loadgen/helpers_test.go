package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/config"
)

const blackBoxTimeout = 10 * time.Second

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

func testRemoteSnapshot(t *testing.T) runtimeSnapshot {
	t.Helper()
	return runtimeSnapshot{
		Run:    runSnapshot{State: runStateRunning},
		Reader: readerSnapshot{WorkerStates: []readerWorkerState{}},
		Sender: senderSnapshot{WorkerStates: []senderWorkerState{}},
	}
}

const (
	profileFixture = "testdata/profile.toml"
	testSenderURL  = "http://127.0.0.1:8081/internal/ingest"
)

func testConfig(t *testing.T) config.Config {
	t.Helper()
	loaded, err := config.Load(profileFixture)
	if err != nil {
		t.Fatalf("load test config: %v", err)
	}
	return loaded
}

func testConfigContents(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(profileFixture)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

func writeConfigInput(t *testing.T, input string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
