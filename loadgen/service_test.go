package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"
	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/pipeline"

	"github.com/parquet-go/parquet-go"
	"go.uber.org/zap"
)

func TestPipelineOptionsPreservesConfiguration(t *testing.T) {
	for _, test := range []struct {
		name      string
		installed bool
	}{
		{name: "throttler installed", installed: true},
		{name: "throttler bypassed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.Reader.ReadBatchSize = 1500
			cfg.ReaderChannel.Capacity = 3
			cfg.SenderChannel.Capacity = 2
			cfg.Reader.Workers.Initial = 4
			cfg.Sender.Workers.Initial, cfg.Sender.Workers.Max = 7, 112
			cfg.Throttler.Installed = test.installed
			cfg.Throttler.RequestedTPS.Initial = 125_000
			cfg.Sender.API.URL = "https://receiver.example.test/ingest"
			cfg.Sender.API.TimeoutMS = 1234
			cfg.Sender.Retry.JitterPercent = 15
			cfg.Sender.Retry.DelaysMS = []int{70, 300, 1100}
			options := pipelineOptions(cfg, nil)
			if options.BatchSize != cfg.Reader.ReadBatchSize || options.ReaderCapacity != cfg.ReaderChannel.Capacity ||
				options.SenderCapacity != cfg.SenderChannel.Capacity || options.Readers != cfg.Reader.Workers.Initial ||
				options.Senders != cfg.Sender.Workers.Initial || options.SenderMaxConns != cfg.Sender.Workers.Max ||
				options.Rate.TPS != cfg.Throttler.RequestedTPS.Initial || options.Rate.Installed != cfg.Throttler.Installed ||
				options.SenderURL != cfg.Sender.API.URL || options.SenderTimeout != 1234*time.Millisecond ||
				options.RetryJitterPercent != cfg.Sender.Retry.JitterPercent ||
				!slices.Equal(options.RetryDelays, []time.Duration{70 * time.Millisecond, 300 * time.Millisecond, 1100 * time.Millisecond}) {
				t.Fatalf("pipeline options = %+v, config = %+v", options, cfg)
			}
		})
	}
}

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
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close listener: %v", err)
		}
	})

	configPath := filepath.Join(t.TempDir(), "config.toml")
	cfg := testConfig()
	cfg.HTTP.Addr = listener.Addr().String()
	cfg.Source.Path = filepath.Join(t.TempDir(), "missing.parquet")
	writeTestConfig(t, configPath, cfg)
	if err := runServe(t.Context(), configPath); !errors.Is(err, addressInUseError()) {
		t.Fatalf("runServe error = %v, want listener startup failure", err)
	}
}

func TestRunServeDrainsAndReleasesListener(t *testing.T) {
	delivered := make(chan struct{}, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		select {
		case delivered <- struct{}{}:
		default:
		}
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
	cfg := testConfig()
	cfg.HTTP.Addr = addr
	cfg.Source.Path = source
	cfg.Sender.API.URL = receiver.URL
	cfg.Throttler.Installed = false
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	var runErr error
	configPath := filepath.Join(t.TempDir(), "config.toml")
	writeTestConfig(t, configPath, cfg)
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
	select {
	case <-delivered:
	case <-done:
		t.Fatalf("runServe exited before delivery: %v", runErr)
	case <-time.After(blackBoxTimeout):
		t.Fatal("runServe did not begin delivery")
	}
	client := &http.Client{Timeout: time.Second}
	snapshotResponse, err := client.Get("http://" + addr + api.SnapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := api.DecodeSnapshot(snapshotResponse.Body)
	snapshotCloseErr := snapshotResponse.Body.Close()
	if snapshotResponse.StatusCode != http.StatusOK || err != nil || snapshotCloseErr != nil || snapshot.Run.State != api.RunStateRunning {
		t.Fatalf("running snapshot = %+v, status=%d, decode=%v, close=%v", snapshot, snapshotResponse.StatusCode, err, snapshotCloseErr)
	}
	wantSpec, err := os.ReadFile("openapi/loadgen.openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get("http://" + addr + "/developer/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	gotSpec, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if response.StatusCode != http.StatusOK || readErr != nil || closeErr != nil || !bytes.Equal(gotSpec, wantSpec) {
		t.Fatalf("embedded OpenAPI document differs from source: status=%d read=%v close=%v", response.StatusCode, readErr, closeErr)
	}
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
