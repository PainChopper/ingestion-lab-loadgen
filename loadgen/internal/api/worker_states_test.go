package api

import (
	"strings"
	"testing"
)

func TestDecodeStrictSnapshotWorkerStates(t *testing.T) {
	for _, pool := range []string{"reader", "sender"} {
		t.Run(pool, func(t *testing.T) {
			for _, test := range []struct {
				name, list string
				valid      bool
			}{
				{name: "empty", list: `[]`, valid: true},
				{name: "duplicates", list: `[{"activity":"idle","draining":false},{"activity":"idle","draining":false}]`, valid: true},
				{name: "null list", list: `null`},
				{name: "object list", list: `{}`},
				{name: "null item", list: `[null]`},
				{name: "unknown activity", list: `[{"activity":"unknown","draining":false}]`},
				{name: "missing activity", list: `[{"draining":false}]`},
				{name: "null activity", list: `[{"activity":null,"draining":false}]`},
				{name: "numeric activity", list: `[{"activity":1,"draining":false}]`},
				{name: "missing draining", list: `[{"activity":"idle"}]`},
				{name: "null draining", list: `[{"activity":"idle","draining":null}]`},
				{name: "string draining", list: `[{"activity":"idle","draining":"false"}]`},
				{name: "extra id", list: `[{"activity":"idle","draining":false,"id":1}]`},
			} {
				t.Run(test.name, func(t *testing.T) {
					body := `{"run":{"state":"running"},"` + pool + `":{"workerStates":` + test.list + `}}`
					_, err := DecodeSnapshot(strings.NewReader(body))
					if (err == nil) != test.valid {
						t.Fatalf("decode error = %v, want valid=%t", err, test.valid)
					}
				})
			}
			for _, activity := range []string{"reading", "blocked", "inFlight", "backoff"} {
				body := `{"run":{"state":"running"},"` + pool + `":{"workerStates":[{"activity":"` + activity + `","draining":true}]}}`
				_, err := DecodeSnapshot(strings.NewReader(body))
				valid := pool == "reader" && (activity == "reading" || activity == "blocked") ||
					pool == "sender" && (activity == "inFlight" || activity == "backoff")
				if (err == nil) != valid {
					t.Errorf("activity %s: error = %v, want valid=%t", activity, err, valid)
				}
			}
		})
	}
}
