package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"
)

func TestRunCLIReportsExactJSONTypeErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		endpoint string
		body     string
		want     string
	}{
		{
			name: "config field", endpoint: api.ConfigPath, body: `{"readerWorkersMax":"bad"}`,
			want: "decode config: json: cannot unmarshal string into Go struct field .readerWorkersMax of type int",
		},
		{
			name: "config root", endpoint: api.ConfigPath, body: `[]`,
			want: "decode config: json: cannot unmarshal array into Go value of type main.configResponse",
		},
		{
			name: "snapshot root", endpoint: api.SnapshotPath, body: `[]`,
			want: "decode snapshot: json: cannot unmarshal array into Go value of type main.runtimeSnapshot",
		},
		{
			name: "run object", endpoint: api.SnapshotPath, body: `{"run":false}`,
			want: "decode snapshot: json: cannot unmarshal bool into Go struct field .run of type main.runSnapshot",
		},
		{
			name: "run state", endpoint: api.SnapshotPath, body: `{"run":{"state":false}}`,
			want: "decode snapshot: json: cannot unmarshal bool into Go struct field .run.state of type main.runState",
		},
		{
			name: "reader object", endpoint: api.SnapshotPath, body: `{"reader":false}`,
			want: "decode snapshot: json: cannot unmarshal bool into Go struct field .reader of type main.readerSnapshot",
		},
		{
			name: "source error", endpoint: api.SnapshotPath, body: `{"reader":{"sourceError":false}}`,
			want: "decode snapshot: json: cannot unmarshal bool into Go struct field .reader.sourceError of type main.readerSourceError",
		},
		{
			name: "source error field", endpoint: api.SnapshotPath, body: `{"reader":{"sourceError":{"path":false}}}`,
			want: "decode snapshot: json: cannot unmarshal bool into Go struct field .reader.sourceError.path of type string",
		},
		{
			name: "throttler object", endpoint: api.SnapshotPath, body: `{"throttler":false}`,
			want: "decode snapshot: json: cannot unmarshal bool into Go struct field .throttler of type main.throttlerSnapshot",
		},
		{
			name: "sender object", endpoint: api.SnapshotPath, body: `{"sender":false}`,
			want: "decode snapshot: json: cannot unmarshal bool into Go struct field .sender of type main.senderSnapshot",
		},
		{
			name: "channel object", endpoint: api.SnapshotPath, body: `{"senderChannel":false}`,
			want: "decode snapshot: json: cannot unmarshal bool into Go struct field .senderChannel of type main.channelSnapshot",
		},
		{
			name: "reader workers", endpoint: api.SnapshotPath, body: `{"reader":{"workerStates":{}}}`,
			want: "decode snapshot: reader.workerStates: json: cannot unmarshal object into Go value of type []main.workerStateItem",
		},
		{
			name: "sender worker", endpoint: api.SnapshotPath, body: `{"sender":{"workerStates":[false]}}`,
			want: "decode snapshot: sender.workerStates: json: cannot unmarshal bool into .0 of type main.workerStateItem",
		},
		{
			name: "worker field", endpoint: api.SnapshotPath, body: `{"sender":{"workerStates":[{"activity":false}]}}`,
			want: "decode snapshot: sender.workerStates: json: cannot unmarshal bool into Go struct field .0.activity of type string",
		},
		{
			name: "literal field", endpoint: api.ConfigPath, body: `{"api.ConfigResponse":1}`,
			want: `decode config: json: unknown field "api.ConfigResponse"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := "{}"
				if r.URL.Path == tc.endpoint {
					body = tc.body
				}
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			var stdout, stderr bytes.Buffer
			code := Run([]string{"status", "--url", server.URL}, &stdout, &stderr, nil)
			want := "GET " + tc.endpoint + ": " + tc.want + "\n"
			if code != cliExitRemote || stdout.Len() != 0 || stderr.String() != want {
				t.Fatalf("exit = %d, stdout = %q, stderr = %q, want stderr %q", code, stdout.String(), stderr.String(), want)
			}
		})
	}
}
