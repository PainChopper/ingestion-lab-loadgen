package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunCLIExitCodes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"--help"}, &stdout, &stderr); code != cliExitSuccess || stdout.String() != cliUsage+"\n" || stderr.Len() != 0 {
		t.Fatalf("help exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if got := runCLI([]string{"status", "--url", "ftp://example.test"}, &stdout, &stderr); got != cliExitUsage {
		t.Fatalf("invalid URL exit = %d, want %d", got, cliExitUsage)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "usage: ingestion-lab-loadgen status [--url <url>]") {
		t.Fatalf("invalid URL stdout/stderr = %q / %q", stdout.String(), stderr.String())
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "conflict", http.StatusConflict)
	}))
	defer server.Close()
	for _, args := range [][]string{{"status", "--url", server.URL}, {"set", "tps", "0", "--url", server.URL}} {
		t.Run(args[0]+" HTTP conflict", func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := runCLI(args, &stdout, &stderr); got != cliExitHTTP {
				t.Fatalf("HTTP exit = %d, want %d", got, cliExitHTTP)
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), "HTTP 409") {
				t.Fatalf("stdout/stderr = %q / %q", stdout.String(), stderr.String())
			}
		})
	}
	stdout.Reset()
	stderr.Reset()
	if got := runCLI([]string{"set", "tps", "not-an-integer"}, &stdout, &stderr); got != cliExitUsage {
		t.Fatalf("invalid set value exit = %d, want %d", got, cliExitUsage)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), cliSetUsage) {
		t.Fatalf("stdout/stderr = %q / %q", stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if got := runCLI([]string{"serve", "--config", "missing-config.toml"}, &stdout, &stderr); got != cliExitLocal {
		t.Fatalf("local startup exit = %d, want %d", got, cliExitLocal)
	}
}

func TestRunCLIReportsRemoteProtocolAndTransportErrors(t *testing.T) {
	malformedServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{"unexpected":true}`))
	}))
	defer malformedServer.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve transport test address: %v", err)
	}
	transportURL := "http://" + listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release transport test address: %v", err)
	}

	for _, test := range []struct {
		name       string
		url        string
		wantStderr string
	}{
		{name: "malformed snapshot", url: malformedServer.URL, wantStderr: "decode snapshot"},
		{name: "transport failure", url: transportURL, wantStderr: "GET /api/loadgen/snapshot"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := runCLI([]string{"status", "--url", test.url}, &stdout, &stderr); got != cliExitRemote {
				t.Fatalf("exit = %d, want %d", got, cliExitRemote)
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), test.wantStderr) {
				t.Fatalf("stdout/stderr = %q / %q", stdout.String(), stderr.String())
			}
		})
	}
}
