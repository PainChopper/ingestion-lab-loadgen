package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInternalTestIngestHandler(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		content    string
		body       string
		wantStatus int
		wantAllow  string
	}{
		{
			name:       "accepts non-empty array",
			method:     http.MethodPost,
			content:    "application/json",
			body:       `[{"client_id":"client-1"}]`,
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "accepts content type parameters",
			method:     http.MethodPost,
			content:    "application/json; charset=utf-8",
			body:       `[{"client_id":"client-1"}]`,
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "rejects wrong method",
			method:     http.MethodGet,
			content:    "application/json",
			body:       `[{"client_id":"client-1"}]`,
			wantStatus: http.StatusMethodNotAllowed,
			wantAllow:  http.MethodPost,
		},
		{
			name:       "rejects missing content type",
			method:     http.MethodPost,
			body:       `[{"client_id":"client-1"}]`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "rejects non-json content type",
			method:     http.MethodPost,
			content:    "text/plain",
			body:       `[{"client_id":"client-1"}]`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "rejects malformed json",
			method:     http.MethodPost,
			content:    "application/json",
			body:       `[`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "rejects empty body",
			method:     http.MethodPost,
			content:    "application/json",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "rejects empty array",
			method:     http.MethodPost,
			content:    "application/json",
			body:       `[]`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "rejects non-array",
			method:     http.MethodPost,
			content:    "application/json",
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "rejects second document",
			method:     http.MethodPost,
			content:    "application/json",
			body:       `[{"client_id":"client-1"}] []`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "rejects oversized body",
			method:     http.MethodPost,
			content:    "application/json",
			body:       `["` + strings.Repeat("a", internalTestIngestMaxBodySize) + `"]`,
			wantStatus: http.StatusRequestEntityTooLarge,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(test.method, internalTestIngestPath, strings.NewReader(test.body))
			req.Header.Set("Content-Type", test.content)
			recorder := httptest.NewRecorder()

			internalTestIngestHandler().ServeHTTP(recorder, req)

			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
			if allow := recorder.Header().Get("Allow"); allow != test.wantAllow {
				t.Fatalf("Allow = %q, want %q", allow, test.wantAllow)
			}
			if test.wantStatus == http.StatusNoContent && recorder.Body.Len() != 0 {
				t.Fatalf("success body length = %d, want 0", recorder.Body.Len())
			}
		})
	}
}
