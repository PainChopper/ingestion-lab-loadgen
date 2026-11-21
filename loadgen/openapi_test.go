package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

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
		"ReaderWorkerState": reflect.TypeOf(readerWorkerState{}),
		"SenderWorkerState": reflect.TypeOf(senderWorkerState{}),
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
		if property["type"] != "array" || property["nullable"] == true {
			t.Errorf("%s worker states must be a non-null array", name)
		}
		if _, exists := property["uniqueItems"]; exists {
			t.Errorf("%s must permit repeated states", name)
		}
		checkFieldSchema(t, name+"[]", field.Elem(), property["items"].(map[string]any), schemas, models)
		return
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
				Sender: senderSnapshot{WorkerStates: []senderWorkerState{}},
				Run:    runSnapshot{State: runStateStarting}, Reader: readerSnapshot{ReadBatchSize: 1000, WorkerStates: []readerWorkerState{}},
				Config: snapshotConfig(testConfig(t)),
			},
			"populated": {
				Run:       runSnapshot{State: runStateStopping, TotalTransactions: 46, ElapsedMs: 1234},
				Reader:    readerSnapshot{Workers: 1, WorkerStates: []readerWorkerState{{Activity: readerWireReading}, {Activity: readerWireBlocked, Draining: true}}, ReadBatchSize: 1500, ReadTps: 123.5, RowsRead: 47, SourceDirectory: "data/part"},
				Throttler: throttlerSnapshot{RequestedTps: 200, AdmittedTps: 3, Installed: true},
				Sender:    senderSnapshot{Workers: 32, WorkerStates: []senderWorkerState{}},
				Config:    snapshotConfig(testConfig(t)),
			},
			"source error": {
				Sender: senderSnapshot{WorkerStates: []senderWorkerState{}},
				Run:    runSnapshot{State: runStateFaulted},
				Config: snapshotConfig(testConfig(t)),
				Reader: readerSnapshot{WorkerStates: []readerWorkerState{}, ReadBatchSize: 1000, SourceError: &readerSourceError{Category: "source", Operation: "read", RelativePath: "input.parquet", Message: "corrupt parquet"}},
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

func TestOpenAPIWorkerStates(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(openAPISpec, &document); err != nil {
		t.Fatal(err)
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	for _, pool := range []struct {
		name       string
		activities []string
	}{
		{name: "Reader", activities: []string{string(readerWireIdle), string(readerWireReading), string(readerWireBlocked)}},
		{name: "Sender", activities: []string{string(senderWireIdle), string(senderWireInFlight), string(senderWireBackoff)}},
	} {
		t.Run(pool.name, func(t *testing.T) {
			item := schemas[pool.name+"WorkerState"].(map[string]any)
			activity := item["properties"].(map[string]any)["activity"].(map[string]any)
			if got := anyStrings(activity["enum"].([]any)); !reflect.DeepEqual(got, pool.activities) {
				t.Errorf("activity enum = %v, want %v", got, pool.activities)
			}
			list := schemas[pool.name+"Status"].(map[string]any)["properties"].(map[string]any)["workerStates"].(map[string]any)
			for _, test := range []struct {
				name, data string
				valid      bool
			}{
				{name: "empty", data: `[]`, valid: true},
				{name: "duplicates", data: `[{"activity":"idle","draining":false},{"activity":"idle","draining":false}]`, valid: true},
				{name: "null list", data: `null`},
				{name: "null item", data: `[null]`},
				{name: "missing activity", data: `[{"draining":false}]`},
				{name: "null activity", data: `[{"activity":null,"draining":false}]`},
				{name: "missing draining", data: `[{"activity":"idle"}]`},
				{name: "null draining", data: `[{"activity":"idle","draining":null}]`},
				{name: "unknown activity", data: `[{"activity":"unknown","draining":false}]`},
				{name: "extra property", data: `[{"activity":"idle","draining":false,"id":1}]`},
			} {
				t.Run(test.name, func(t *testing.T) {
					var value any
					if err := json.Unmarshal([]byte(test.data), &value); err != nil {
						t.Fatal(err)
					}
					if err := validateSchema(value, list, schemas); (err == nil) != test.valid {
						t.Fatalf("schema error = %v, want valid=%t", err, test.valid)
					}
				})
			}
			for _, activity := range []string{"reading", "blocked", "inFlight", "backoff"} {
				value := []any{map[string]any{"activity": activity, "draining": true}}
				err := validateSchema(value, list, schemas)
				if (err == nil) != slices.Contains(pool.activities, activity) {
					t.Errorf("%s activity validation error = %v", activity, err)
				}
			}
		})
	}
	data, err := os.ReadFile("testdata/snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture any
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if err := validateSchema(fixture, schemas["Snapshot"].(map[string]any), schemas); err != nil {
		t.Fatalf("shared fixture violates snapshot schema: %v", err)
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
