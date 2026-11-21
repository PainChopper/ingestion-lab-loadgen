package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
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
		Config: snapshotConfig(testConfig(t)),
	}
}

const testSenderURL = "http://127.0.0.1:8081/internal/ingest"

func testConfig(t *testing.T) config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(testConfigContents()), 0o600); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	loaded, err := loadConfig(path)
	if err != nil {
		t.Fatalf("load test config: %v", err)
	}
	return loaded
}

func testConfigContents() string {
	return `[http]
addr = "127.0.0.1:8080"

[source]
path = 'C:\dataset\*.parquet'

[reader]
read_batch_size = 1000

[reader.workers]
initial = 1
max = 7

[readerChannel]
capacity = 2

[senderChannel]
capacity = 0

[throttler]
installed = true

[throttler.requested_tps]
initial = 2000000
max = 4000000
step = 200000


[sender.workers]
initial = 32
max = 32

[sender.api]
timeout_ms = 30000
url = "http://127.0.0.1:8081/internal/ingest"

[sender.retry]
# After the final delay, retries continue at that delay until delivery succeeds
# or the operator explicitly stops or resets the run.
delays_ms = [250, 500, 1000, 2000, 5000]
jitter_percent = 20

[metrics]
window_ms = 1000

[logging]
level = "info"
`
}

func writeConfigInput(t *testing.T, input string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
