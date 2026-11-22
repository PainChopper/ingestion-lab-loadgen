package api

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestDecodeStrictSnapshotValidatesJSONAndState(t *testing.T) {
	for _, test := range []struct {
		name, body string
		wantErr    bool
	}{
		{name: "partial status", body: `{"run":{"state":"running"}}`},
		{name: "starting", body: `{"run":{"state":"starting"}}`},
		{name: "stopping", body: `{"run":{"state":"stopping"}}`},
		{name: "faulted", body: `{"run":{"state":"faulted"}}`},
		{name: "missing and null unrelated values", body: `{"run":{"state":"running"},"reader":null,"throttler":{"requestedTps":null,"installed":null}}`},
		{name: "missing state", body: `{"run":{}}`, wantErr: true},
		{name: "null state", body: `{"run":{"state":null}}`, wantErr: true},
		{name: "invalid state", body: `{"run":{"state":"invalid"}}`, wantErr: true},
		{name: "old reader rate", body: `{"run":{"state":"running"},"reader":{"readTps":1}}`, wantErr: true},
		{name: "old throttler rate", body: `{"run":{"state":"running"},"throttler":{"admittedTps":1}}`, wantErr: true},
		{name: "old input rate", body: `{"run":{"state":"running"},"readerChannel":{"inputTransactionsPerSecond":1}}`, wantErr: true},
		{name: "old output rate", body: `{"run":{"state":"running"},"senderChannel":{"outputTransactionsPerSecond":1}}`, wantErr: true},
		{name: "reader batch size", body: `{"run":{"state":"running"},"reader":{"readBatchSize":1}}`, wantErr: true},
		{name: "source directory", body: `{"run":{"state":"running"},"reader":{"sourceDirectory":"data"}}`, wantErr: true},
		{name: "reader capacity", body: `{"run":{"state":"running"},"readerChannel":{"capacity":1}}`, wantErr: true},
		{name: "sender capacity", body: `{"run":{"state":"running"},"senderChannel":{"capacity":1}}`, wantErr: true},
		{name: "reader buffered transactions", body: `{"run":{"state":"running"},"readerChannel":{"bufferedTransactions":1}}`, wantErr: true},
		{name: "sender buffered transactions", body: `{"run":{"state":"running"},"senderChannel":{"bufferedTransactions":1}}`, wantErr: true},
		{name: "source error category", body: `{"run":{"state":"running"},"reader":{"sourceError":{"category":"source"}}}`, wantErr: true},
		{name: "source error relative path", body: `{"run":{"state":"running"},"reader":{"sourceError":{"relativePath":"a.parquet"}}}`, wantErr: true},
		{name: "embedded config", body: `{"run":{"state":"running"},"config":{}}`, wantErr: true},
		{name: "unknown root field", body: `{"run":{"state":"running"},"unexpected":true}`, wantErr: true},
		{name: "unknown nested field", body: `{"run":{"state":"running"},"reader":{"unexpected":true}}`, wantErr: true},
		{name: "wrong known type", body: `{"run":{"state":"running"},"throttler":{"requestedTps":"0"}}`, wantErr: true},
		{name: "array root", body: `[]`, wantErr: true},
		{name: "null root", body: `null`, wantErr: true},
		{name: "two objects", body: `{"run":{"state":"running"}} {}`, wantErr: true},
		{name: "trailing null", body: `{"run":{"state":"running"}} null`, wantErr: true},
		{name: "trailing garbage", body: `{"run":{"state":"running"}} broken`, wantErr: true},
		{name: "malformed JSON", body: `{"run":`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeSnapshot(strings.NewReader(test.body))
			if (err != nil) != test.wantErr {
				t.Fatalf("decode error=%v, wantErr=%v", err, test.wantErr)
			}
		})
	}
}

func TestDecodeStrictConfig(t *testing.T) {
	data, err := os.ReadFile("../../testdata/config.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	want := ConfigResponse{
		ReaderWorkersMax: 7, SenderWorkersMax: 32, RequestedTpsMax: 4000000,
		ReadBatchSize: 20, ReaderChannelCapacity: 51, SenderChannelCapacity: 71, SourcePath: "reader-source",
	}
	if got != want {
		t.Fatalf("decoded config = %+v, want %+v", got, want)
	}
	for _, test := range []struct{ name, body string }{
		{name: "unknown field", body: `{"initial":1}`},
		{name: "wrong type", body: `{"readBatchSize":"20"}`},
		{name: "array", body: `[]`},
		{name: "null", body: `null`},
		{name: "malformed", body: `{`},
		{name: "second object", body: string(data) + `{}`},
		{name: "trailing null", body: string(data) + `null`},
		{name: "trailing garbage", body: string(data) + `broken`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodeConfig(strings.NewReader(test.body)); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}
