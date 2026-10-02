package receiver

import (
	"net/http"
	"strings"
	"testing"
)

func TestNewServerResolvesOptionalResponseStatus(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		input   *int
		want    int
		wantErr bool
	}{
		{name: "absent", want: http.StatusNoContent},
		{name: "explicit zero", line: "response_status = 0", input: new(0), want: http.StatusNoContent},
		{name: "success", line: "response_status = 204", input: new(204), want: http.StatusNoContent},
		{name: "bad request", line: "response_status = 400", input: new(400), want: http.StatusBadRequest},
		{name: "server error", line: "response_status = 500", input: new(500), want: http.StatusInternalServerError},
		{name: "unsupported", line: "response_status = 201", input: new(201), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := strings.Replace(validTOMLConfig(), "response_status = 204", test.line, 1)
			config, err := LoadConfig(writeConfig(t, content))
			if test.wantErr {
				if err == nil {
					t.Fatal("unsupported toml response status accepted")
				}
				config, err = LoadConfig(writeConfig(t, validTOMLConfig()))
				if err != nil {
					t.Fatal(err)
				}
				config.Lab.ResponseStatus = test.input
				if _, err := NewServer(config); err == nil {
					t.Fatal("unsupported programmatic response status accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (config.Lab.ResponseStatus == nil) != (test.input == nil) {
				t.Fatal("toml input presence changed")
			}
			server, err := NewServer(config)
			if err != nil {
				t.Fatal(err)
			}
			assertStatus(t, server, 0, 0, false, 25, test.want)

			server = newTestServer(t, 1024, LabConfig{ResponseStatus: test.input})
			if test.input != nil {
				*test.input = 201
			}
			recorder := performRequest(
				server,
				httpRequest(http.MethodPost, "/internal/ingest", "application/json", validBatch()),
			)
			if recorder.Code != test.want {
				t.Fatalf("ingest status = %d, want %d", recorder.Code, test.want)
			}
			var accepted int64
			if test.want == http.StatusNoContent {
				accepted = 1
			}
			assertStatus(t, server, accepted, accepted, accepted != 0, 0, test.want)
		})
	}
}
