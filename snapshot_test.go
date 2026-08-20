package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSnapshotHandlerReturnsOwnerSnapshot(t *testing.T) {
	commands := make(chan command)
	expected := statusSnapshot{RunState: runStateRunning, TotalTransactions: 46, ReaderWorkers: 1, SenderWorkers: 0}

	req := httptest.NewRequest(http.MethodGet, snapshotPath, nil)
	rec := httptest.NewRecorder()

	fakeStateOwner := func() {
		cmd := <-commands
		if cmd.kind != getSnapshot {
			t.Errorf("command kind = %v, want %v", cmd.kind, getSnapshot)
		}
		cmd.snapshotReply <- expected
	}
	go fakeStateOwner()

	snapshotHandler(commands).ServeHTTP(rec, req)
	response := rec.Result()
	if response.StatusCode != http.StatusOK {
		t.Errorf("reply code = %v, want %v", response.StatusCode, http.StatusOK)
	}
	contentType := response.Header.Get("Content-Type")
	const contentTypeName = "application/json"
	if contentType != contentTypeName {
		t.Errorf("content type = %v, want %v", contentType, contentTypeName)
	}

	var actual statusSnapshot
	if err := json.NewDecoder(response.Body).Decode(&actual); err != nil {
		t.Fatalf("decode response body: %v", err)
	}

	if actual != expected {
		t.Errorf("actual = %v, want %v", actual, expected)
	}
}

func TestSnapshotHandlerRejectsPOST(t *testing.T) {
	commands := make(chan command)

	req := httptest.NewRequest(http.MethodPost, snapshotPath, nil)
	rec := httptest.NewRecorder()

	snapshotHandler(commands).ServeHTTP(rec, req)
	response := rec.Result()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("reply code = %v, want %v", rec.Code, http.StatusMethodNotAllowed)
	}
	allow := response.Header.Get("Allow")
	if allow != http.MethodGet {
		t.Errorf("allow header = %q, want %q", allow, http.MethodGet)
	}
}

func TestServeMuxRoutesSnapshot(t *testing.T) {
	commands := make(chan command)
	mux := newServeMux(commands, nil)

	req := httptest.NewRequest(http.MethodGet, snapshotPath, nil)
	_, pattern := mux.Handler(req)

	if pattern != snapshotPath {
		t.Errorf("pattern = %q, want %q", pattern, snapshotPath)
	}
}
