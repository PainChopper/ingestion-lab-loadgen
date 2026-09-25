package main

import (
	"bytes"
	"strings"
	"testing"

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
	var output bytes.Buffer
	logger, err := newApplicationLogger("warn", &output)
	if err != nil {
		t.Fatalf("newApplicationLogger() error = %v", err)
	}

	logger.Info("successful batch", zap.String("event", "batch_delivery_succeeded"))
	logger.Warn("delivery failed", zap.String("event", "batch_delivery_failed"))

	line := output.String()
	if strings.Contains(line, "batch_delivery_succeeded") {
		t.Fatalf("filtered successful event was written: %q", line)
	}
	if !strings.Contains(line, "event=batch_delivery_failed") {
		t.Fatalf("warn event was not written: %q", line)
	}
}
