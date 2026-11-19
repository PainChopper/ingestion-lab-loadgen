package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"loadgen/internal/pipeline"

	"github.com/parquet-go/parquet-go"
	"go.uber.org/zap"
)

func TestNewApplicationLoggerWritesLogfmtToProvidedStdout(t *testing.T) {
	var output bytes.Buffer
	logger, err := newApplicationLogger("info", &output)
	if err != nil {
		t.Fatalf("newApplicationLogger() error = %v", err)
	}

	logger.Info("service started", zap.String("event", "service_started"), zap.String("detail", "a=b \"quoted\"\nnext"))

	line := output.String()
	for _, field := range []string{"ts=", "level=info", "msg=\"service started\"", "event=service_started"} {
		if !strings.Contains(line, field) {
			t.Errorf("log line %q does not contain %q", line, field)
		}
	}
	if strings.Count(line, "\n") != 1 {
		t.Errorf("log line contains more than one record: %q", line)
	}
}

func TestNewApplicationLoggerRejectsInvalidLevel(t *testing.T) {
	if _, err := newApplicationLogger("verbose", &bytes.Buffer{}); err == nil {
		t.Fatal("newApplicationLogger() error = nil, want validation error")
	}
}

func TestNewApplicationLoggerFiltersBelowConfiguredLevel(t *testing.T) {
	for _, level := range []string{"info", "warn"} {
		t.Run(level, func(t *testing.T) {
			var output bytes.Buffer
			logger, err := newApplicationLogger(level, &output)
			if err != nil {
				t.Fatalf("newApplicationLogger() error = %v", err)
			}

			logger.Info("successful batch", zap.String("event", "batch_delivery_succeeded"))
			logger.Warn("delivery failed", zap.String("event", "batch_delivery_failed"))
			for _, event := range []string{"reader_worker_started", "reader_source_opened", "sender_worker_stopped"} {
				logger.Debug("worker lifecycle", zap.String("event", event))
			}

			line := output.String()
			if strings.Contains(line, "batch_delivery_succeeded") != (level == "info") {
				t.Fatalf("filtered successful event was written: %q", line)
			}
			if !strings.Contains(line, "event=batch_delivery_failed") {
				t.Fatalf("warn event was not written: %q", line)
			}
			for _, event := range []string{"reader_worker_started", "reader_source_opened", "sender_worker_stopped"} {
				if strings.Contains(line, event) {
					t.Fatalf("info/warn logger emitted debug lifecycle event: %q", line)
				}
			}
		})
	}
}

func TestRunServeFailsBeforePipelineWhenListenerOccupied(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve HTTP listener: %v", err)
	}
	defer listener.Close()

	configPath := filepath.Join(t.TempDir(), "config.toml")
	content := strings.Replace(testConfigContents(), `addr = "127.0.0.1:8080"`, `addr = "`+listener.Addr().String()+`"`, 1)
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := runServe(context.Background(), configPath); err == nil || !strings.Contains(err.Error(), "listen HTTP server") {
		t.Fatalf("runServe error = %v, want listener startup failure", err)
	}
}

func TestRunServeDrainsAndReleasesListener(t *testing.T) {
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(receiver.Close)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "input.parquet")
	if err := parquet.WriteFile(source, []pipeline.Transaction{{ClientID: "drain"}}); err != nil {
		t.Fatal(err)
	}
	input := strings.NewReplacer(
		"127.0.0.1:8080", addr,
		`C:\dataset\*.parquet`, filepath.ToSlash(source),
		testSenderURL, receiver.URL,
		"installed = true", "installed = false",
		"initial = 32", "initial = 1",
	).Replace(testConfigContents())
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	var runErr error
	configPath := writeConfigInput(t, input)
	go func() {
		runErr = runServe(ctx, configPath)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("runServe did not stop after cancellation")
		}
	})
	waitForHTTPSnapshotState(t, "http://"+addr, runStateRunning)
	cancel()
	select {
	case <-done:
		if runErr != nil {
			t.Fatalf("runServe: %v", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runServe did not drain")
	}
	rebound, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listener still occupied after runServe returned: %v", err)
	}
	if err := rebound.Close(); err != nil {
		t.Fatal(err)
	}
}
