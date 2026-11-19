package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestCommandsHandlerRejectsGet(t *testing.T) {
	commands := make(chan runtimeRequest, 1)
	req := httptest.NewRequest(http.MethodGet, commandsPath, nil)
	rec := httptest.NewRecorder()

	newServeMux(runtimeRequests{queue: commands}, newPrometheusMetrics(), testConfig(t), zap.NewNop()).ServeHTTP(rec, req)
	response := rec.Result()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("reply code = %v, want %v", rec.Code, http.StatusMethodNotAllowed)
	}
	allow := response.Header.Get("Allow")
	if allow != http.MethodPost {
		t.Errorf("allow header = %q, want %q", allow, http.MethodPost)
	}
	if rec.Body.String() != "Method Not Allowed\n" {
		t.Errorf("405 body=%q", rec.Body.String())
	}
}

func TestCommandsHandlerRejectsInvalidRequest(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"malformed-json", "malformed-json"},
		{"unknown-action", `{"action":"unknown-action","value":2}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commands := make(chan runtimeRequest, 1)
			body := strings.NewReader(test.body)
			req := httptest.NewRequest(http.MethodPost, commandsPath, body)
			rec := httptest.NewRecorder()
			newServeMux(runtimeRequests{queue: commands}, newPrometheusMetrics(), testConfig(t), zap.NewNop()).ServeHTTP(rec, req)
			response := rec.Result()
			if response.StatusCode != http.StatusBadRequest {
				t.Errorf("reply code = %v, want %v", response.StatusCode, http.StatusBadRequest)
			}
		})
	}
}

func TestCommandsHandlerRejectsOversizedBody(t *testing.T) {
	const command = `{"action":"set-reader-workers","value":2}`
	const limit = 4096
	tests := []struct {
		name string
		body string
	}{
		{"malformed JSON exceeds limit", "!" + strings.Repeat("x", limit)},
		{"JSON exceeds limit", `{"action":"` + strings.Repeat("x", limit) + `","value":2}`},
		{"trailing whitespace exceeds limit", command + strings.Repeat(" ", limit+1-len(command))},
	}
	done := make(chan struct{})
	close(done)
	handler := newServeMux(runtimeRequests{done: done}, newPrometheusMetrics(), testConfig(t), zap.NewNop())
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, commandsPath, strings.NewReader(test.body))
			req.ContentLength = -1
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
			}
		})
	}
}

func TestThrottlerCommandValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		want      int
		kind      runtimeCommandKind
		installed bool
	}{
		{name: "TPS missing", body: `{"action":"set-requested-tps"}`, want: http.StatusBadRequest},
		{name: "TPS null", body: `{"action":"set-requested-tps","value":null}`, want: http.StatusBadRequest},
		{name: "TPS fractional", body: `{"action":"set-requested-tps","value":100.5}`, want: http.StatusBadRequest},
		{name: "TPS string", body: `{"action":"set-requested-tps","value":"100"}`, want: http.StatusBadRequest},
		{name: "TPS negative", body: `{"action":"set-requested-tps","value":-100}`, want: http.StatusBadRequest},
		{name: "TPS off step", body: `{"action":"set-requested-tps","value":101}`, want: http.StatusBadRequest},
		{name: "TPS over max", body: `{"action":"set-requested-tps","value":4100000}`, want: http.StatusBadRequest},
		{name: "TPS extra field", body: `{"action":"set-requested-tps","value":100,"other":1}`, want: http.StatusBadRequest},
		{name: "TPS trailing JSON", body: `{"action":"set-requested-tps","value":100}{}`, want: http.StatusBadRequest},
		{name: "mode missing", body: `{"action":"set-throttler-installed"}`, want: http.StatusBadRequest},
		{name: "mode null", body: `{"action":"set-throttler-installed","value":null}`, want: http.StatusBadRequest},
		{name: "mode number", body: `{"action":"set-throttler-installed","value":1}`, want: http.StatusBadRequest},
		{name: "mode unknown", body: `{"action":"set-throttler-installed","value":"other"}`, want: http.StatusBadRequest},
		{name: "old installed string", body: `{"action":"set-throttler-installed","value":"installed"}`, want: http.StatusBadRequest},
		{name: "old bypass string", body: `{"action":"set-throttler-installed","value":"bypass"}`, want: http.StatusBadRequest},
		{name: "quoted bool", body: `{"action":"set-throttler-installed","value":"false"}`, want: http.StatusBadRequest},
		{name: "installed trailing JSON", body: `{"action":"set-throttler-installed","value":false}{}`, want: http.StatusBadRequest},
		{name: "mode extra field", body: `{"action":"set-throttler-installed","value":"bypass","other":1}`, want: http.StatusBadRequest},
		{name: "TPS zero", body: `{"action":"set-requested-tps","value":0}`, want: http.StatusOK, kind: setRequestedTPS},
		{name: "TPS initial", body: `{"action":"set-requested-tps","value":2000000}`, want: http.StatusOK, kind: setRequestedTPS},
		{name: "TPS maximum", body: `{"action":"set-requested-tps","value":4000000}`, want: http.StatusOK, kind: setRequestedTPS},
		{name: "mode installed", body: `{"action":"set-throttler-installed","value":true}`, want: http.StatusOK, kind: setThrottlerInstalled, installed: true},
		{name: "mode bypass", body: `{"action":"set-throttler-installed","value":false}`, want: http.StatusOK, kind: setThrottlerInstalled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commands := make(chan runtimeRequest, 1)
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, commandsPath, strings.NewReader(test.body))
			done := make(chan struct{})
			config := testConfig(t)
			go func() {
				defer close(done)
				newServeMux(runtimeRequests{queue: commands}, newPrometheusMetrics(), config, zap.NewNop()).ServeHTTP(recorder, req)
			}()
			if test.want == http.StatusOK {
				select {
				case request := <-commands:
					dispatch := request.(controlRequest)
					command := dispatch.command
					if command.kind != test.kind {
						t.Errorf("command kind = %v, want %v", command.kind, test.kind)
					}
					if command.kind == setThrottlerInstalled && command.installed != test.installed {
						t.Errorf("installed = %t, want %t", command.installed, test.installed)
					}
					dispatch.reply <- true
				case <-time.After(time.Second):
					t.Fatal("valid command was not dispatched")
				}
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("command handler did not return")
			}
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d", recorder.Code, test.want)
			}
			if test.want == http.StatusBadRequest && len(commands) != 0 {
				t.Fatal("invalid command was dispatched")
			}
		})
	}
}

func TestSenderCommandValidation(t *testing.T) {
	tests := []struct {
		name, body string
		want       int
		kind       runtimeCommandKind
		value      int
	}{
		{"workers minimum", `{"action":"set-sender-workers","value":1}`, http.StatusOK, setSenderWorkers, 1},
		{"workers initial", `{"action":"set-sender-workers","value":32}`, http.StatusOK, setSenderWorkers, 32},
		{"missing", `{"action":"set-sender-workers"}`, http.StatusBadRequest, 0, 0},
		{"null", `{"action":"set-sender-workers","value":null}`, http.StatusBadRequest, 0, 0},
		{"fractional", `{"action":"set-sender-workers","value":1.5}`, http.StatusBadRequest, 0, 0},
		{"string", `{"action":"set-sender-workers","value":"1"}`, http.StatusBadRequest, 0, 0},
		{"under minimum", `{"action":"set-sender-workers","value":0}`, http.StatusBadRequest, 0, 0},
		{"over maximum", `{"action":"set-sender-workers","value":33}`, http.StatusBadRequest, 0, 0},
		{"extra key", `{"action":"set-sender-workers","value":1,"other":1}`, http.StatusBadRequest, 0, 0},
		{"trailing JSON", `{"action":"set-sender-workers","value":1}{}`, http.StatusBadRequest, 0, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commands := make(chan runtimeRequest, 1)
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, commandsPath, strings.NewReader(test.body))
			done := make(chan struct{})
			go func() {
				defer close(done)
				newServeMux(runtimeRequests{queue: commands}, newPrometheusMetrics(), testConfig(t), zap.NewNop()).ServeHTTP(recorder, req)
			}()
			if test.want == http.StatusOK {
				select {
				case request := <-commands:
					dispatch := request.(controlRequest)
					command := dispatch.command
					if command.kind != test.kind || command.value != test.value {
						t.Errorf("command = %+v, want kind %v value %d", command, test.kind, test.value)
					}
					dispatch.reply <- true
				case <-time.After(time.Second):
					t.Fatal("valid command was not dispatched")
				}
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("command handler did not return")
			}
			if recorder.Code != test.want || len(commands) != 0 {
				t.Fatalf("status = %d, queued commands = %d, want %d and zero", recorder.Code, len(commands), test.want)
			}
		})
	}
}

func TestReaderWorkersCommandValidation(t *testing.T) {
	const command = `{"action":"set-reader-workers","value":1}`
	tests := []struct {
		name, body string
		want       int
		value      int
	}{
		{"minimum", command, http.StatusOK, 1},
		{"body at the limit", command + strings.Repeat(" ", 4096-len(command)), http.StatusOK, 1},
		{"maximum", `{"action":"set-reader-workers","value":7}`, http.StatusOK, 7},
		{"zero", `{"action":"set-reader-workers","value":0}`, http.StatusBadRequest, 0},
		{"eight", `{"action":"set-reader-workers","value":8}`, http.StatusBadRequest, 0},
		{"missing", `{"action":"set-reader-workers"}`, http.StatusBadRequest, 0},
		{"null", `{"action":"set-reader-workers","value":null}`, http.StatusBadRequest, 0},
		{"fractional", `{"action":"set-reader-workers","value":1.5}`, http.StatusBadRequest, 0},
		{"string", `{"action":"set-reader-workers","value":"1"}`, http.StatusBadRequest, 0},
		{"extra key", `{"action":"set-reader-workers","value":1,"other":1}`, http.StatusBadRequest, 0},
		{"trailing JSON", `{"action":"set-reader-workers","value":1}{}`, http.StatusBadRequest, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commands := make(chan runtimeRequest, 1)
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, commandsPath, strings.NewReader(test.body))
			done := make(chan struct{})
			go func() {
				defer close(done)
				newServeMux(runtimeRequests{queue: commands}, newPrometheusMetrics(), testConfig(t), zap.NewNop()).ServeHTTP(recorder, req)
			}()
			if test.want == http.StatusOK {
				select {
				case request := <-commands:
					dispatch := request.(controlRequest)
					command := dispatch.command
					if command.kind != setReaderWorkers || command.value != test.value {
						t.Errorf("command = %+v, want Reader workers %d", command, test.value)
					}
					dispatch.reply <- true
				case <-time.After(time.Second):
					t.Fatal("valid Reader command was not dispatched")
				}
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("Reader command handler did not return")
			}
			if recorder.Code != test.want || len(commands) != 0 {
				t.Fatalf("status = %d, queued = %d, want %d and zero", recorder.Code, len(commands), test.want)
			}
		})
	}
}

func TestDeveloperRoutes(t *testing.T) {
	requests := make(chan runtimeRequest, 1)
	mux := newServeMux(runtimeRequests{queue: requests}, newPrometheusMetrics(), testConfig(t), zap.NewNop())
	tests := []struct {
		name        string
		path        string
		contentType string
		contains    string
	}{
		{"spec", developerOpenAPIPath, "application/json", `"openapi": "3.0.3"`},
		{"swagger", developerSwaggerUIPath, "text/html; charset=utf-8", `swagger-ui-bundle.js`},
		{"swagger initializer", developerSwaggerUIPath + "swagger-initializer.js", "application/javascript", developerOpenAPIPath},
		{"swagger bundle", developerSwaggerUIPath + "swagger-ui-bundle.js", "application/javascript", "SwaggerUIBundle"},
		{"swagger stylesheet", developerSwaggerUIPath + "swagger-ui.css", "text/css; charset=utf-8", "swagger-ui"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", recorder.Code)
			}
			if got := recorder.Header().Get("Content-Type"); got != test.contentType {
				t.Errorf("Content-Type = %q, want %q", got, test.contentType)
			}
			if !bytes.Contains(recorder.Body.Bytes(), []byte(test.contains)) {
				t.Errorf("body does not contain %q", test.contains)
			}
		})
	}
	if len(requests) != 0 {
		t.Fatal("developer GET dispatched a runtime command")
	}
	specRecorder := httptest.NewRecorder()
	mux.ServeHTTP(specRecorder, httptest.NewRequest(http.MethodGet, developerOpenAPIPath, nil))
	if !bytes.Equal(specRecorder.Body.Bytes(), openAPISpec) {
		t.Error("served OpenAPI document differs from embedded bytes")
	}
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, developerSwaggerUIPath+"swagger-initializer.js", nil))
	for _, setting := range []string{`tryItOutEnabled: blankToUndefinedBool('true')`, `validatorUrl: blankToUndefined('none')`} {
		if !strings.Contains(recorder.Body.String(), setting) {
			t.Errorf("Swagger UI config is missing %q", setting)
		}
	}
}

func TestOpenAPIContract(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(openAPISpec, &document); err != nil {
		t.Fatal(err)
	}
	if got := document["openapi"]; got != "3.0.3" {
		t.Errorf("openapi = %v", got)
	}
	servers := document["servers"].([]any)
	if len(servers) != 1 || servers[0].(map[string]any)["url"] != "/" {
		t.Errorf("servers = %v, want one same-origin root", servers)
	}
	paths := document["paths"].(map[string]any)
	if !reflect.DeepEqual(stringSet(mapKeys(paths)), stringSet([]string{snapshotPath, commandsPath})) {
		t.Errorf("paths = %v", mapKeys(paths))
	}
	if !reflect.DeepEqual(mapKeys(paths[snapshotPath].(map[string]any)), []string{"get", "head"}) ||
		!reflect.DeepEqual(mapKeys(paths[commandsPath].(map[string]any)), []string{"post"}) {
		t.Errorf("methods do not match GET/HEAD snapshot and POST commands")
	}
	components := document["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)
	for name, rawSchema := range schemas {
		if schema, ok := rawSchema.(map[string]any); ok {
			if err := checkSchemaKeywords(schema); err != nil {
				t.Errorf("schema %s: %v", name, err)
			}
		}
	}
	responses := components["responses"].(map[string]any)
	checkLocalReferences(t, document, schemas, responses)
	checkCommandsSchema(t, paths[commandsPath].(map[string]any)["post"].(map[string]any), schemas)
	checkSnapshotSchema(t, schemas)
	checkResponseSemantics(t, paths, responses, schemas)
}

func checkLocalReferences(t *testing.T, node any, schemas, responses map[string]any) {
	t.Helper()
	switch value := node.(type) {
	case map[string]any:
		if reference, ok := value["$ref"].(string); ok {
			if len(value) != 1 {
				t.Errorf("reference has ignored siblings: %v", value)
			}
			switch {
			case strings.HasPrefix(reference, "#/components/schemas/"):
				if _, ok := schemas[strings.TrimPrefix(reference, "#/components/schemas/")]; !ok {
					t.Errorf("missing referenced schema %q", reference)
				}
			case strings.HasPrefix(reference, "#/components/responses/"):
				if _, ok := responses[strings.TrimPrefix(reference, "#/components/responses/")]; !ok {
					t.Errorf("missing referenced response %q", reference)
				}
			default:
				t.Errorf("unsupported reference %q", reference)
			}
		}
		for _, child := range value {
			checkLocalReferences(t, child, schemas, responses)
		}
	case []any:
		for _, child := range value {
			checkLocalReferences(t, child, schemas, responses)
		}
	}
}

func checkCommandsSchema(t *testing.T, operation map[string]any, schemas map[string]any) {
	t.Helper()
	content := operation["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)
	examples := content["examples"].(map[string]any)
	command := schemas["Command"].(map[string]any)
	branches := command["oneOf"].([]any)
	discriminator := command["discriminator"].(map[string]any)
	if discriminator["propertyName"] != "action" {
		t.Errorf("discriminator propertyName = %v, want action", discriminator["propertyName"])
	}
	mapping := discriminator["mapping"].(map[string]any)
	if len(branches) != 4 || len(examples) != 4 || len(mapping) != 4 {
		t.Fatalf("branches/examples/mapping = %d/%d/%d, want 4", len(branches), len(examples), len(mapping))
	}
	seen := map[string]bool{}
	expected := map[string]runtimeCommand{
		"set-reader-workers":      {kind: setReaderWorkers, value: 2},
		"set-requested-tps":       {kind: setRequestedTPS, value: 2000000},
		"set-throttler-installed": {kind: setThrottlerInstalled, installed: true},
		"set-sender-workers":      {kind: setSenderWorkers, value: 32},
	}
	if got := stringSet(mapKeys(mapping)); !reflect.DeepEqual(got, stringSet(mapKeysFromRuntimeCommands(expected))) {
		t.Errorf("discriminator actions = %v, want actions accepted by commandsHandler %v", mapKeys(mapping), mapKeysFromRuntimeCommands(expected))
	}
	for _, example := range examples {
		body := example.(map[string]any)["value"].(map[string]any)
		action := body["action"].(string)
		if seen[action] {
			t.Errorf("duplicate example action %q", action)
		}
		seen[action] = true
		want, knownAction := expected[action]
		if !knownAction {
			t.Errorf("example action %q is not accepted by commandsHandler", action)
			continue
		}
		assertExampleDispatches(t, body, want)
		matches := 0
		for _, branch := range branches {
			reference := branch.(map[string]any)["$ref"].(string)
			name := strings.TrimPrefix(reference, "#/components/schemas/")
			schema := schemas[name].(map[string]any)
			properties := schema["properties"].(map[string]any)
			actions := properties["action"].(map[string]any)["enum"].([]any)
			if len(actions) != 1 {
				t.Errorf("%s action enum = %v", name, actions)
				continue
			}
			if actions[0] != action {
				continue
			}
			if err := validateSchema(body, schema, schemas); err != nil {
				t.Errorf("example %q does not satisfy %s: %v", action, name, err)
				continue
			}
			matches++
			if mapping[action] != reference {
				t.Errorf("mapping for %q = %v, want %q", action, mapping[action], reference)
			}
			required := stringSet(anyStrings(schema["required"].([]any)))
			if _, ok := required["action"]; !ok {
				t.Errorf("%s does not require action", name)
			}
			_, hasValue := body["value"]
			_, requiresValue := required["value"]
			if hasValue != requiresValue {
				t.Errorf("%s example value presence = %t, required = %t", name, hasValue, requiresValue)
			}
			if schema["additionalProperties"] != false {
				t.Errorf("%s does not reject unknown fields", name)
			}
			if requiresValue && action != "set-throttler-installed" {
				key, ok := schema["x-config-key"].(string)
				if !ok || key == "" {
					t.Errorf("%s has no config key for console", name)
				} else if _, ok := schemas["ConfigStatus"].(map[string]any)["properties"].(map[string]any)[key]; !ok {
					t.Errorf("%s references absent config field %q", name, key)
				}
				valueType := properties["value"].(map[string]any)["type"]
				switch valueType {
				case "integer":
					if number, ok := body["value"].(float64); !ok || number != float64(int64(number)) {
						t.Errorf("%s example value is not integer: %v", name, body["value"])
					}
				case "string":
					if _, ok := body["value"].(string); !ok {
						t.Errorf("%s example value is not string: %v", name, body["value"])
					}
				case "boolean":
					if _, ok := body["value"].(bool); !ok {
						t.Errorf("%s example value is not boolean: %v", name, body["value"])
					}
				default:
					t.Errorf("%s unexpected value type %v", name, valueType)
				}
			}
		}
		if matches != 1 {
			t.Errorf("example %q matches %d oneOf branches, want 1", action, matches)
		}
	}
}

func mapKeysFromRuntimeCommands(commands map[string]runtimeCommand) []string {
	keys := make([]string, 0, len(commands))
	for action := range commands {
		keys = append(keys, action)
	}
	slices.Sort(keys)
	return keys
}

func assertExampleDispatches(t *testing.T, body map[string]any, want runtimeCommand) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan runtimeRequest, 1)
	commandConfig := testConfig(t)
	request := httptest.NewRequest(http.MethodPost, commandsPath, bytes.NewReader(encoded))
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		newServeMux(runtimeRequests{queue: requests}, newPrometheusMetrics(), commandConfig, zap.NewNop()).ServeHTTP(recorder, request)
	}()
	var dispatched controlRequest
	select {
	case queued := <-requests:
		dispatched = queued.(controlRequest)
	case <-time.After(time.Second):
		t.Fatalf("example %q was not dispatched by commandsHandler", body["action"])
	}
	got := dispatched.command
	if got.kind != want.kind || got.value != want.value || got.installed != want.installed {
		t.Errorf("example %q dispatched %+v, want kind %v value %d installed %t", body["action"], got, want.kind, want.value, want.installed)
	}
	dispatched.reply <- true
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("handler did not complete for example %q", body["action"])
	}
	if recorder.Code != http.StatusOK {
		t.Errorf("example %q returned HTTP %d, want 200", body["action"], recorder.Code)
	}
}

func validateSchema(value any, schema map[string]any, schemas map[string]any) error {
	if err := checkSchemaKeywords(schema); err != nil {
		return err
	}
	if reference, ok := schema["$ref"].(string); ok {
		if len(schema) != 1 {
			return fmt.Errorf("$ref siblings are unsupported")
		}
		name := strings.TrimPrefix(reference, "#/components/schemas/")
		target, ok := schemas[name].(map[string]any)
		if !ok || name == reference {
			return fmt.Errorf("unsupported schema reference %q", reference)
		}
		return validateSchema(value, target, schemas)
	}
	if alternatives, ok := schema["oneOf"].([]any); ok {
		matches := 0
		for _, alternative := range alternatives {
			branch, ok := alternative.(map[string]any)
			if !ok {
				return fmt.Errorf("oneOf branch is not a schema")
			}
			if validateSchema(value, branch, schemas) == nil {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("oneOf matched %d branches, want 1", matches)
		}
		return nil
	}
	if value == nil && schema["nullable"] == true {
		return nil
	}
	if value, ok := value.(float64); ok {
		if minimum, ok := schema["minimum"].(float64); ok && value < minimum {
			return errors.New("value below minimum")
		}
		if maximum, ok := schema["maximum"].(float64); ok && value > maximum {
			return errors.New("value above maximum")
		}
	}
	switch schema["type"] {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("got %T, want object", value)
		}
		properties, _ := schema["properties"].(map[string]any)
		for _, required := range anyStringsOrEmpty(schema["required"]) {
			if _, ok := object[required]; !ok {
				return fmt.Errorf("missing required property %q", required)
			}
		}
		for key, child := range object {
			property, ok := properties[key].(map[string]any)
			if !ok {
				if schema["additionalProperties"] == false {
					return fmt.Errorf("unexpected property %q", key)
				}
				continue
			}
			if err := validateSchema(child, property, schemas); err != nil {
				return fmt.Errorf("property %q: %w", key, err)
			}
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("got %T, want string", value)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("got %T, want boolean", value)
		}
	case "integer":
		number, ok := value.(float64)
		if !ok || number != float64(int64(number)) {
			return fmt.Errorf("got %v, want integer", value)
		}
	case "number":
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("got %T, want number", value)
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("got %T, want array", value)
		}
		itemSchema, ok := schema["items"].(map[string]any)
		if !ok {
			return fmt.Errorf("array has no items schema")
		}
		for index, item := range items {
			if err := validateSchema(item, itemSchema, schemas); err != nil {
				return fmt.Errorf("item %d: %w", index, err)
			}
		}
	default:
		return fmt.Errorf("unsupported or missing type %v", schema["type"])
	}
	if values, ok := schema["enum"].([]any); ok && !slices.Contains(values, value) {
		return fmt.Errorf("value %v is not in enum %v", value, values)
	}
	return nil
}

func checkSchemaKeywords(schema map[string]any) error {
	keywords := map[string]bool{
		"$ref": true, "type": true, "enum": true, "format": true, "description": true,
		"nullable": true, "additionalProperties": true, "properties": true, "required": true,
		"minimum": true, "maximum": true, "items": true, "oneOf": true, "discriminator": true, "x-config-key": true,
	}
	for keyword := range schema {
		if !keywords[keyword] {
			return fmt.Errorf("unsupported schema keyword %q", keyword)
		}
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		for name, rawProperty := range properties {
			if property, ok := rawProperty.(map[string]any); ok {
				if err := checkSchemaKeywords(property); err != nil {
					return fmt.Errorf("property %q: %w", name, err)
				}
			}
		}
	}
	if items, ok := schema["items"].(map[string]any); ok {
		if err := checkSchemaKeywords(items); err != nil {
			return fmt.Errorf("items: %w", err)
		}
	}
	if alternatives, ok := schema["oneOf"].([]any); ok {
		for index, rawAlternative := range alternatives {
			if alternative, ok := rawAlternative.(map[string]any); ok {
				if err := checkSchemaKeywords(alternative); err != nil {
					return fmt.Errorf("oneOf[%d]: %w", index, err)
				}
			}
		}
	}
	return nil
}

func anyStringsOrEmpty(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	return anyStrings(values)
}

func checkSnapshotSchema(t *testing.T, schemas map[string]any) {
	t.Helper()
	models := map[string]reflect.Type{
		"Snapshot":          reflect.TypeOf(runtimeSnapshot{}),
		"RunStatus":         reflect.TypeOf(runSnapshot{}),
		"ReaderStatus":      reflect.TypeOf(readerSnapshot{}),
		"ReaderSourceError": reflect.TypeOf(readerSourceError{}),
		"ThrottlerStatus":   reflect.TypeOf(throttlerSnapshot{}),
		"SenderStatus":      reflect.TypeOf(senderSnapshot{}),
		"ChannelStatus":     reflect.TypeOf(channelSnapshot{}),
		"ConfigStatus":      reflect.TypeOf(configSnapshot{}),
		"WorkersConfig":     reflect.TypeOf(workersConfig{}),
		"TPSConfig":         reflect.TypeOf(tpsConfig{}),
	}
	for name, model := range models {
		schema := schemas[name].(map[string]any)
		if schema["type"] != "object" || schema["additionalProperties"] != false {
			t.Errorf("%s must be a closed object", name)
		}
		properties := schema["properties"].(map[string]any)
		required := stringSet(anyStrings(schema["required"].([]any)))
		if len(properties) != model.NumField() || len(required) != model.NumField() {
			t.Errorf("%s fields/properties/required = %d/%d/%d", name, model.NumField(), len(properties), len(required))
		}
		for field := range model.Fields() {
			key := strings.Split(field.Tag.Get("json"), ",")[0]
			property, ok := properties[key].(map[string]any)
			if !ok {
				t.Errorf("%s missing property %q", name, key)
				continue
			}
			if _, ok := required[key]; !ok {
				t.Errorf("%s does not require %q", name, key)
			}
			checkFieldSchema(t, name+"."+key, field.Type, property, schemas, models)
		}
	}
}

func checkFieldSchema(t *testing.T, name string, field reflect.Type, property map[string]any, schemas map[string]any, models map[string]reflect.Type) {
	t.Helper()
	if reference, ok := property["$ref"].(string); ok {
		targetName := strings.TrimPrefix(reference, "#/components/schemas/")
		target := schemas[targetName].(map[string]any)
		if field.Kind() == reflect.Pointer {
			if target["nullable"] != true {
				t.Errorf("%s pointer reference is not nullable", name)
			}
			field = field.Elem()
		}
		if field.Kind() != reflect.Struct {
			t.Errorf("%s references object but Go type is %v", name, field)
		} else if models[targetName] != field {
			t.Errorf("%s references %s, want schema for %v", name, targetName, field)
		}
		return
	}
	if field.Kind() == reflect.Slice {
		if property["type"] != "array" || property["nullable"] != true {
			t.Errorf("%s slice must allow array or null", name)
		}
		field = field.Elem()
		property = property["items"].(map[string]any)
	}
	want := "integer"
	switch field.Kind() {
	case reflect.String:
		want = "string"
	case reflect.Bool:
		want = "boolean"
	case reflect.Float64:
		want = "number"
	case reflect.Int, reflect.Int64:
	default:
		t.Errorf("%s unsupported Go type %v", name, field)
		return
	}
	if property["type"] != want {
		t.Errorf("%s type = %v, want %s", name, property["type"], want)
	}
	if field.Kind() == reflect.Int64 && property["format"] != "int64" {
		t.Errorf("%s missing int64 format", name)
	}
	if field.Kind() == reflect.Float64 && property["format"] != "double" {
		t.Errorf("%s missing double format", name)
	}
}

func checkResponseSemantics(t *testing.T, paths map[string]any, responses map[string]any, schemas map[string]any) {
	t.Helper()
	head := paths[snapshotPath].(map[string]any)["head"].(map[string]any)["responses"].(map[string]any)
	if !reflect.DeepEqual(stringSet(mapKeys(head)), stringSet([]string{"200", "405", "503"})) {
		t.Errorf("HEAD response codes=%v", mapKeys(head))
	}
	for code, response := range head {
		if _, ok := response.(map[string]any)["content"]; ok {
			t.Errorf("HEAD %s must have no body", code)
		}
	}
	snapshot := paths[snapshotPath].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)
	commands := paths[commandsPath].(map[string]any)["post"].(map[string]any)["responses"].(map[string]any)
	if !reflect.DeepEqual(stringSet(mapKeys(snapshot)), stringSet([]string{"200", "405", "503"})) ||
		!reflect.DeepEqual(stringSet(mapKeys(commands)), stringSet([]string{"200", "400", "405", "409", "413", "503"})) {
		t.Errorf("snapshot/commands response codes = %v / %v", mapKeys(snapshot), mapKeys(commands))
	}
	snapshotContent, ok := snapshot["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)
	if !ok || snapshotContent["schema"].(map[string]any)["$ref"] != "#/components/schemas/Snapshot" {
		t.Error("snapshot 200 must reference the handler's Snapshot DTO")
	} else {
		fixtures := map[string]runtimeSnapshot{
			"zero": {
				Run: runSnapshot{State: runStateStarting}, Reader: readerSnapshot{ReadBatchSize: 1000},
				Config: snapshotConfig(testConfig(t)),
			},
			"populated": {
				Run:       runSnapshot{State: runStateStopping, TotalTransactions: 46, ElapsedMs: 1234},
				Reader:    readerSnapshot{Workers: 1, LiveWorkers: 2, ReadBatchSize: 1500, ReadTps: 123.5, RowsRead: 47, SourceDirectory: "data/part"},
				Throttler: throttlerSnapshot{RequestedTps: 200, AdmittedTps: 3, Installed: true},
				Sender:    senderSnapshot{Workers: 32},
				Config:    snapshotConfig(testConfig(t)),
			},
			"source error": {
				Run:    runSnapshot{State: runStateFaulted},
				Config: snapshotConfig(testConfig(t)),
				Reader: readerSnapshot{ReadBatchSize: 1000, SourceError: &readerSourceError{Category: "source", Operation: "read", RelativePath: "input.parquet", Message: "corrupt parquet"}},
			},
		}
		for name, fixture := range fixtures {
			encoded, err := json.Marshal(fixture)
			if err != nil {
				t.Fatal(err)
			}
			var value any
			if err := json.Unmarshal(encoded, &value); err != nil {
				t.Fatal(err)
			}
			if err := validateSchema(value, schemas["Snapshot"].(map[string]any), schemas); err != nil {
				t.Errorf("%s snapshot fixture does not satisfy Snapshot: %v", name, err)
			}
		}
	}
	for _, status := range []string{"200", "409"} {
		if _, ok := commands[status].(map[string]any)["content"]; ok {
			t.Errorf("commands %s promises a body", status)
		}
	}
	for _, operation := range []map[string]any{snapshot, commands} {
		if operation["503"].(map[string]any)["$ref"] != "#/components/responses/RuntimeUnavailable" {
			t.Errorf("503 does not document runtime unavailability: %v", operation["503"])
		}
	}
	runSchema := schemas["RunStatus"].(map[string]any)["properties"].(map[string]any)["state"].(map[string]any)
	wantStates := []string{string(runStateStarting), string(runStateRunning), string(runStateStopping), string(runStateFaulted)}
	if got := anyStrings(runSchema["enum"].([]any)); !reflect.DeepEqual(stringSet(got), stringSet(wantStates)) {
		t.Errorf("run state enum = %v, want %v", got, wantStates)
	}
	if _, ok := responses["InvalidCommand"].(map[string]any)["content"].(map[string]any)["text/plain"]; !ok {
		t.Error("commands 400 has no plain-text error")
	}
	if _, ok := responses["MethodNotAllowed"].(map[string]any)["headers"].(map[string]any)["Allow"]; !ok {
		t.Error("405 does not document Allow header")
	}
}

func TestSchemaExampleValidationRejectsContractDrift(t *testing.T) {
	tests := []struct {
		name   string
		schema map[string]any
		value  any
	}{
		{
			name: "action enum",
			schema: map[string]any{"type": "object", "required": []any{"action"}, "properties": map[string]any{
				"action": map[string]any{"type": "string", "enum": []any{"set-reader-workers"}},
			}},
			value: map[string]any{"action": "unknown-action"},
		},
		{
			name: "required property",
			schema: map[string]any{"type": "object", "required": []any{"action", "value"}, "properties": map[string]any{
				"action": map[string]any{"type": "string"}, "value": map[string]any{"type": "integer"},
			}},
			value: map[string]any{"action": "set-reader-workers"},
		},
		{
			name: "unsupported keyword",
			schema: map[string]any{"type": "object", "required": []any{"action"}, "properties": map[string]any{
				"action": map[string]any{"type": "string"},
			}, "patternProperties": map[string]any{}},
			value: map[string]any{"action": "set-reader-workers"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateSchema(test.value, test.schema, map[string]any{}); err == nil {
				t.Fatal("invalid example unexpectedly satisfied schema")
			}
		})
	}
}

func mapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func anyStrings(values []any) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = value.(string)
	}
	return result
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func TestServeMuxHEADUsesSnapshotAndReturnsNoBody(t *testing.T) {
	requests := make(chan runtimeRequest)
	mux := newServeMux(runtimeRequests{queue: requests}, newPrometheusMetrics(), testConfig(t), zap.NewNop())
	server := httptest.NewServer(mux)
	defer server.Close()
	go func() {
		(<-requests).(snapshotRequest).reply <- runtimeSnapshot{Run: runSnapshot{State: runStateRunning}}
	}()
	for _, test := range []struct {
		path, allow string
		status      int
	}{
		{path: snapshotPath, status: http.StatusOK},
		{path: commandsPath, status: http.StatusMethodNotAllowed, allow: http.MethodPost},
	} {
		response, err := server.Client().Head(server.URL + test.path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if err != nil || closeErr != nil || len(body) != 0 || response.StatusCode != test.status || response.Header.Get("Allow") != test.allow {
			t.Fatalf("HEAD %s: status=%d body=%q allow=%q read=%v close=%v", test.path, response.StatusCode, body, response.Header.Get("Allow"), err, closeErr)
		}
		if test.status == http.StatusOK && response.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("HEAD snapshot Content-Type=%q", response.Header.Get("Content-Type"))
		}
	}
}

func TestHTTPWaitFailuresReturnServiceUnavailable(t *testing.T) {
	cfg := testConfig(t)
	for _, path := range []string{snapshotPath, commandsPath} {
		for _, stage := range []string{"send", "reply"} {
			for _, cause := range []string{"cancel", "deadline", "event loop exit"} {
				t.Run(path+"/"+stage+"/"+cause, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
						defer cancel()
						queue := make(chan runtimeRequest)
						loopDone := make(chan struct{})
						mux := newServeMux(runtimeRequests{queue: queue, done: loopDone}, newPrometheusMetrics(), cfg, zap.NewNop())
						method, body := http.MethodGet, ""
						if path == commandsPath {
							method, body = http.MethodPost, `{"action":"set-reader-workers","value":1}`
						}
						request := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
						recorder := httptest.NewRecorder()
						returned := make(chan struct{})
						go func() { mux.ServeHTTP(recorder, request); close(returned) }()
						if stage == "reply" {
							<-queue
						}
						synctest.Wait()
						switch cause {
						case "cancel":
							cancel()
						case "deadline":
							time.Sleep(time.Millisecond)
						case "event loop exit":
							close(loopDone)
						}
						<-returned
						if recorder.Code != http.StatusServiceUnavailable || recorder.Body.String() != "runtime unavailable\n" {
							t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
						}
					})
				})
			}
		}
	}
}

func TestHTTPCommandReceiptsHaveEmptyBody(t *testing.T) {
	for _, applied := range []bool{true, false} {
		requests := make(chan runtimeRequest)
		mux := newServeMux(runtimeRequests{queue: requests}, newPrometheusMetrics(), testConfig(t), zap.NewNop())
		go func() { (<-requests).(controlRequest).reply <- applied }()
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, commandsPath, strings.NewReader(`{"action":"set-reader-workers","value":1}`)))
		want := http.StatusOK
		if !applied {
			want = http.StatusConflict
		}
		if recorder.Code != want || recorder.Body.Len() != 0 {
			t.Fatalf("applied=%t status=%d body=%q", applied, recorder.Code, recorder.Body.String())
		}
	}
}

func TestStartHTTPServerLogsActualListenerAddress(t *testing.T) {
	cfg := testConfig(t)
	// The loader still rejects port zero; directly start the listener to distinguish actual from configured address.
	cfg.HTTP.Addr = "127.0.0.1:0"
	core, logs := observer.New(zap.InfoLevel)
	server, done, err := startHTTPServer(runtimeRequests{}, newPrometheusMetrics(), cfg, zap.New(core))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := shutdownHTTPServer(ctx, server); err != nil {
			t.Error(err)
		}
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	entries := logs.FilterMessage("service started").All()
	if len(entries) != 1 {
		t.Fatalf("service start logs=%d", len(entries))
	}
	fields := entries[0].ContextMap()
	addr, ok := fields["http_addr"].(string)
	if !ok || addr == cfg.HTTP.Addr || fields["event"] != "service_started" {
		t.Fatalf("start fields=%v", fields)
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		t.Fatal(err)
	}
}

func TestServeMuxRoutes(t *testing.T) {
	loaded := testConfig(t)
	loaded.Sender.API.URL = "https://example.test" + snapshotPath
	mux := newServeMux(runtimeRequests{queue: make(chan runtimeRequest, 1)}, newPrometheusMetrics(), loaded, zap.NewNop())

	for _, route := range []struct{ method, path, pattern string }{
		{http.MethodGet, snapshotPath, "GET " + snapshotPath},
		{http.MethodHead, snapshotPath, "GET " + snapshotPath},
		{http.MethodPost, commandsPath, "POST " + commandsPath},
	} {
		_, pattern := mux.Handler(httptest.NewRequest(route.method, route.path, nil))
		if pattern != route.pattern {
			t.Errorf("pattern=%q,want %q", pattern, route.pattern)
		}
	}
}

func TestServeMuxHasNoLocalIngest(t *testing.T) {
	mux := newServeMux(runtimeRequests{queue: make(chan runtimeRequest)}, newPrometheusMetrics(), testConfig(t), zap.NewNop())
	for _, path := range []string{"/internal/test/ingest", "/internal/ingest"} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", path, recorder.Code)
		}
	}
}

func TestStartHTTPServerUsesConfiguredAddressAndShutsDown(t *testing.T) {
	available, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := available.Addr().String()
	if err := available.Close(); err != nil {
		t.Fatal(err)
	}
	loaded := testConfig(t)
	loaded.HTTP.Addr = addr
	server, done, err := startHTTPServer(runtimeRequests{queue: make(chan runtimeRequest)}, newPrometheusMetrics(), loaded, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := shutdownHTTPServer(ctx, server); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("HTTP server did not exit")
		}
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatalf("configured address not released after shutdown: %v", err)
		}
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		t.Logf("shutdown released %s", addr)
	})
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + addr + commandsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("configured server GET = %d, want %d", response.StatusCode, http.StatusMethodNotAllowed)
	}
	t.Logf("configured server %s answered GET %s with %d", addr, commandsPath, response.StatusCode)
}

func TestStartHTTPServerReturnsOccupiedAddressError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	loaded := testConfig(t)
	loaded.HTTP.Addr = listener.Addr().String()
	server, done, err := startHTTPServer(runtimeRequests{queue: make(chan runtimeRequest)}, newPrometheusMetrics(), loaded, zap.NewNop())
	if server != nil || done != nil {
		t.Fatal("failed startup returned server or done channel")
	}
	var listenError *net.OpError
	if !errors.As(err, &listenError) || listenError.Op != "listen" {
		t.Fatalf("startup error = %v, want wrapped listen error", err)
	}
	t.Logf("occupied address %s returned %v", loaded.HTTP.Addr, err)
}

func TestShutdownHTTPServerClosesAnActiveRequestAtDeadline(t *testing.T) {
	entered := make(chan struct{})
	left := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(left)
	}))
	server.Start()
	t.Cleanup(server.Close)
	requestCtx, cancelRequest := context.WithTimeout(t.Context(), time.Second)
	defer cancelRequest()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		response, err := server.Client().Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		result <- err
	}()
	select {
	case <-entered:
	case <-requestCtx.Done():
		t.Fatal("HTTP request did not enter handler")
	}
	shutdownCtx, cancelShutdown := context.WithDeadline(t.Context(), time.Now())
	defer cancelShutdown()
	if err := shutdownHTTPServer(shutdownCtx, server.Config); err != nil {
		t.Fatal(err)
	}
	select {
	case <-left:
	case <-requestCtx.Done():
		t.Fatal("HTTP handler remained active past force-close")
	}
	if err := <-result; err == nil || requestCtx.Err() != nil {
		t.Fatalf("request error = %v, client context = %v, want server-side close", err, requestCtx.Err())
	}
}

type failingSnapshotWriter struct{ *httptest.ResponseRecorder }

func (w failingSnapshotWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestSnapshotEncodingFailureLogsItsOwnEventAndCause(t *testing.T) {
	requests := make(chan runtimeRequest)
	go func() {
		(<-requests).(snapshotRequest).reply <- runtimeSnapshot{Run: runSnapshot{State: runStateRunning}}
	}()
	core, events := observer.New(zap.WarnLevel)
	snapshotHandler(runtimeRequests{queue: requests}, zap.New(core)).ServeHTTP(
		failingSnapshotWriter{ResponseRecorder: httptest.NewRecorder()},
		httptest.NewRequest(http.MethodGet, snapshotPath, nil),
	)
	entries := events.FilterMessage("snapshot response encoding failed").All()
	if len(entries) != 1 || entries[0].ContextMap()["event"] != "snapshot_response_failed" || entries[0].ContextMap()["error"] != io.ErrClosedPipe.Error() {
		t.Fatalf("encoding failure log=%v", entries)
	}
}

func TestSnapshotHandlerReturnsOwnerSnapshot(t *testing.T) {
	requests := make(chan runtimeRequest, 1)
	expected := runtimeSnapshot{
		Run: runSnapshot{State: runStateRunning, TotalTransactions: 46, ElapsedMs: 1234},
		Reader: readerSnapshot{
			Workers: 1, LiveWorkers: 2, ReadingWorkers: 2, DrainingWorkers: 1,
			DrainingReadingWorkers: 1,
			ReadBatchSize:          50000, ReadTps: 123.5, RowsRead: 47,
			SourceDirectory: "data/part", SourceError: &readerSourceError{Category: "source", Operation: "read", RelativePath: "input.parquet", Message: "corrupt parquet"},
		},
		Throttler:     throttlerSnapshot{RequestedTps: 200, AdmittedTps: 3, Installed: true},
		Sender:        senderSnapshot{Workers: 32},
		ReaderChannel: channelSnapshot{Capacity: 8, DepthBatches: 6, BufferedTransactions: 300000, BlockedSenders: 1, OldestBlockedSenderMs: 12, BlockedMs: 34, SentTransactionsPerSecond: 3, ReceivedTransactionsPerSecond: 1},
		SenderChannel: channelSnapshot{Capacity: 16, DepthBatches: 4, BufferedTransactions: 100000, BlockedSenders: 2, OldestBlockedSenderMs: 13, BlockedMs: 35, SentTransactionsPerSecond: 4, ReceivedTransactionsPerSecond: 2.5},
		Config:        snapshotConfig(testConfig(t)),
	}
	rec := httptest.NewRecorder()
	go func() { (<-requests).(snapshotRequest).reply <- expected }()
	snapshotHandler(runtimeRequests{queue: requests}, zap.NewNop()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, snapshotPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("reply code = %d, want %d", rec.Code, http.StatusOK)
	}
	var actual runtimeSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	if got := actual; !reflect.DeepEqual(got, expected) {
		t.Errorf("actual = %+v, want %+v", got, expected)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &root); err != nil {
		t.Fatal(err)
	}
	if string(root["config"]) == "null" {
		t.Error("config must be a JSON object")
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(root["config"], &config); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	var workers workersConfig
	if err := json.Unmarshal(config["readerWorkers"], &workers); err != nil {
		t.Fatal(err)
	}
	if want := (workersConfig{Initial: 1, Max: 7}); workers != want {
		t.Errorf("Reader workers config = %+v, want %+v", workers, want)
	}
	var reader map[string]json.RawMessage
	if err := json.Unmarshal(root["reader"], &reader); err != nil {
		t.Fatal(err)
	}
	if string(reader["liveWorkers"]) != "2" || string(reader["drainingWorkers"]) != "1" {
		t.Errorf("Reader worker counts = %s / %s", reader["liveWorkers"], reader["drainingWorkers"])
	}
}

func TestSnapshotHandlerIncludesZeroAndNullValues(t *testing.T) {
	requests := make(chan runtimeRequest, 1)
	go func() {
		(<-requests).(snapshotRequest).reply <- runtimeSnapshot{Run: runSnapshot{State: runStateStarting}}
	}()
	recorder := httptest.NewRecorder()
	snapshotHandler(runtimeRequests{queue: requests}, zap.NewNop()).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, snapshotPath, nil))
	var root map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &root); err != nil {
		t.Fatal(err)
	}
	var actual runtimeSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	if want := (runtimeSnapshot{Run: runSnapshot{State: runStateStarting}}); !reflect.DeepEqual(actual, want) {
		t.Errorf("zero snapshot = %+v, want %+v", actual, want)
	}
	var run, reader map[string]json.RawMessage
	if err := json.Unmarshal(root["run"], &run); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(root["reader"], &reader); err != nil {
		t.Fatal(err)
	}
	if string(run["elapsedMs"]) != "0" || string(reader["readTps"]) != "0" || string(reader["rowsRead"]) != "0" || string(reader["sourceDirectory"]) != "\"\"" || string(reader["sourceError"]) != "null" {
		t.Errorf("starting snapshot body = %v", root)
	}
	var throttler map[string]json.RawMessage
	if err := json.Unmarshal(root["throttler"], &throttler); err != nil {
		t.Fatal(err)
	}
	if string(throttler["installed"]) != "false" {
		t.Errorf("zero snapshot installed = %s, want false", throttler["installed"])
	}
}

func TestSnapshotHandlerRejectsPost(t *testing.T) {
	requests := make(chan runtimeRequest, 1)
	rec := httptest.NewRecorder()
	newServeMux(runtimeRequests{queue: requests}, newPrometheusMetrics(), testConfig(t), zap.NewNop()).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, snapshotPath, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("reply code = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q, want %q", got, "GET, HEAD")
	}
	if rec.Body.String() != "Method Not Allowed\n" {
		t.Errorf("405 body=%q", rec.Body.String())
	}
}
