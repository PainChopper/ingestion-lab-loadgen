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
)

type logWriterFunc func([]byte) (int, error)

func (f logWriterFunc) Write(data []byte) (int, error) { return f(data) }

func TestRunLogsLifecycleAndReleasesListener(t *testing.T) {
	configPath := receiverConfigAt(t, "127.0.0.1:0")
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
	defer listener.Close()
	var output bytes.Buffer
	err = run(t.Context(), receiverConfigAt(t, listener.Addr().String()), &output)
	if err == nil || !strings.Contains(output.String(), "event=http_server_failed") || strings.Contains(output.String(), "event=service_started") {
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
			<-entered
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

func receiverConfigAt(t *testing.T, addr string) string {
	t.Helper()
	content, err := os.ReadFile("config.toml")
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(content), `address = "127.0.0.1:8081"`, `address = "`+addr+`"`, 1)
	if updated == string(content) {
		t.Fatal("configured address replacement did not apply")
	}
	path := filepath.Join(t.TempDir(), "receiver.toml")
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
