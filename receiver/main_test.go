package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PainChopper/ingestion-lab-loadgen/receiver/internal/config"
	"github.com/PainChopper/ingestion-lab-loadgen/receiver/internal/ingest"
	"github.com/pelletier/go-toml/v2"
)

type logWriterFunc func([]byte) (int, error)

func (f logWriterFunc) Write(data []byte) (int, error) { return f(data) }

func TestSampleConfigLoads(t *testing.T) {
	if _, err := config.Load("config.toml"); err != nil {
		t.Fatalf("load sample config: %v", err)
	}
}

func TestRunPreservesMissingConfigCause(t *testing.T) {
	var output bytes.Buffer
	err := run(t.Context(), filepath.Join(t.TempDir(), "missing.toml"), &output)
	if !errors.Is(err, os.ErrNotExist) || output.Len() != 0 {
		t.Fatalf("run error=%v output=%q, want missing config without startup", err, output.String())
	}
}

func TestRunLogsLifecycleAndReleasesListener(t *testing.T) {
	configPath := writeRunConfig(t, testConfig())
	var output bytes.Buffer
	started := make(chan string, 1)
	writer := logWriterFunc(func(data []byte) (int, error) {
		n, err := output.Write(data)
		if strings.Contains(string(data), "event=service_started") {
			for _, field := range strings.Fields(string(data)) {
				if strings.HasPrefix(field, "http_addr=") {
					started <- strings.TrimPrefix(field, "http_addr=")
				}
			}
		}
		return n, err
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, configPath, writer) }()
	var addr string
	select {
	case addr = <-started:
	case err := <-done:
		t.Fatalf("receiver exited before startup: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("receiver did not report startup")
	}
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + addr + "/status")
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if response.StatusCode != http.StatusOK || readErr != nil || closeErr != nil {
		t.Fatalf("status=%d read=%v close=%v", response.StatusCode, readErr, closeErr)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"service_started", "service_stopping", "service_stopped"} {
		if strings.Count(output.String(), "event="+event) != 1 {
			t.Fatalf("lifecycle event %s missing or duplicated: %s", event, output.String())
		}
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("receiver left listener occupied: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRunLogsBindFailureWithoutStartedEvent(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close listener: %v", err)
		}
	})
	var output bytes.Buffer
	cfg := testConfig()
	cfg.Server.Address = listener.Addr().String()
	err = run(t.Context(), writeRunConfig(t, cfg), &output)
	var bindErr *net.OpError
	if !errors.As(err, &bindErr) || bindErr.Op != "listen" {
		t.Fatalf("run error=%v, want listener error", err)
	}
	if !strings.Contains(output.String(), "event=http_server_failed") || strings.Contains(output.String(), "event=service_started") {
		t.Fatalf("bind error=%v logs=%s", err, output.String())
	}
}

func TestShutdownHTTPServerDrainsOrForceClosesActiveRequest(t *testing.T) {
	for _, mode := range []string{"graceful", "forced"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			entered, release, left := make(chan struct{}), make(chan struct{}), make(chan struct{})
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				if mode == "forced" {
					<-r.Context().Done()
				} else {
					<-release
					w.WriteHeader(http.StatusNoContent)
				}
				close(left)
			})}
			t.Cleanup(func() { _ = server.Close() })
			done := make(chan error, 1)
			serveFinished := make(chan struct{})
			go func() {
				err := server.Serve(listener)
				if errors.Is(err, http.ErrServerClosed) {
					err = nil
				}
				done <- err
				close(serveFinished)
			}()
			clientDone := make(chan error, 1)
			go func() {
				client := &http.Client{Timeout: 3 * time.Second}
				response, err := client.Get("http://" + listener.Addr().String())
				if response != nil {
					if response.StatusCode != http.StatusNoContent {
						t.Errorf("graceful response=%d", response.StatusCode)
					}
					_ = response.Body.Close()
				}
				clientDone <- err
			}()
			select {
			case <-entered:
			case err := <-clientDone:
				t.Fatalf("client finished before handler entry: %v", err)
			}
			var logs bytes.Buffer
			logger, err := newApplicationLogger("info", &logs)
			if err != nil {
				t.Fatal(err)
			}
			timeout := time.Second
			if mode == "forced" {
				timeout = 0
			}
			shutdownDone := make(chan error, 1)
			go func() { shutdownDone <- shutdownHTTPServer(server, done, timeout, logger) }()
			<-serveFinished
			if mode == "graceful" {
				close(release)
			}
			err = <-shutdownDone
			clientErr := <-clientDone
			<-left
			if mode == "forced" {
				if !errors.Is(err, context.DeadlineExceeded) || clientErr == nil || !strings.Contains(logs.String(), "event=http_shutdown_failed") {
					t.Fatalf("forced close: shutdown=%v client=%v logs=%s", err, clientErr, logs.String())
				}
			} else if err != nil || clientErr != nil || logs.Len() != 0 {
				t.Fatalf("graceful drain: shutdown=%v client=%v logs=%s", err, clientErr, logs.String())
			}
		})
	}
}

func TestApplicationLoggerHonorsConfiguredLevel(t *testing.T) {
	var output bytes.Buffer
	logger, err := newApplicationLogger("warn", &output)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("hidden")
	logger.Warn("visible")
	if strings.Contains(output.String(), "hidden") || !strings.Contains(output.String(), "visible") {
		t.Fatalf("level filtering failed: %s", output.String())
	}
}

func testConfig() config.Config {
	return config.Config{
		Server:  config.Server{Address: "127.0.0.1:0", ShutdownTimeout: 2 * time.Second},
		Ingest:  ingest.Config{Path: "/internal/ingest", MaxBodyBytes: 1024},
		Logging: config.Logging{Level: "info"},
	}
}

func writeRunConfig(t *testing.T, cfg config.Config) string {
	t.Helper()

	// run takes a TOML path, so write the test settings using the file schema.
	lab := map[string]any{"response_delay_ms": cfg.Ingest.ResponseDelay.Milliseconds()}
	if cfg.Ingest.ResponseStatus != nil {
		lab["response_status"] = *cfg.Ingest.ResponseStatus
	}
	content, err := toml.Marshal(map[string]any{
		"server": map[string]any{
			"address":             cfg.Server.Address,
			"shutdown_timeout_ms": cfg.Server.ShutdownTimeout.Milliseconds(),
		},
		"ingest":  map[string]any{"path": cfg.Ingest.Path, "max_body_bytes": cfg.Ingest.MaxBodyBytes},
		"logging": map[string]any{"level": cfg.Logging.Level},
		"lab":     lab,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "receiver.toml")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
