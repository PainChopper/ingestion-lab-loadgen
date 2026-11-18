package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"
)

type remoteHTTPError struct {
	status   string
	method   string
	endpoint string
	detail   string
}

func (err remoteHTTPError) Error() string {
	message := fmt.Sprintf("HTTP %s %s %s", err.status, err.method, err.endpoint)
	if err.detail != "" {
		return message + ": " + err.detail
	}
	return message
}

func runRemoteCLI(ctx context.Context, command cliCommand, stdout io.Writer) error {
	client := remoteCLIClient{baseURL: command.remoteURL, httpClient: &http.Client{Timeout: remoteClientTimeout}}
	if command.remoteAction == "status" {
		snapshot, err := client.snapshot(ctx)
		if err != nil {
			return err
		}
		_, err = io.WriteString(stdout, formatRuntimeStatusCard(snapshot))
		return err
	}
	if command.remoteAction == "set" {
		if err := client.commandWithValue(ctx, command.remoteSet.requestAction, command.remoteSet.value); err != nil {
			return err
		}
		snapshot, err := client.snapshot(ctx)
		if err != nil {
			return err
		}
		if err := command.remoteSet.confirm(snapshot); err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "action=set target=%s value=%v state=%s\n", command.remoteSet.target, command.remoteSet.value, snapshot.Run.State)
		return err
	}

	return fmt.Errorf("unknown remote action %q", command.remoteAction)
}

type remoteCLIClient struct {
	baseURL    string
	httpClient *http.Client
}

func (client remoteCLIClient) snapshot(ctx context.Context) (runtimeStatus, error) {
	response, err := client.do(ctx, http.MethodGet, snapshotPath, nil)
	if err != nil {
		return runtimeStatus{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return runtimeStatus{}, newRemoteHTTPError(response, http.MethodGet, snapshotPath)
	}
	return decodeStrictSnapshot(response.Body)
}

func (client remoteCLIClient) commandWithValue(ctx context.Context, action string, value any) error {
	body, err := json.Marshal(struct {
		Action string `json:"action"`
		Value  any    `json:"value,omitempty"`
	}{Action: action, Value: value})
	if err != nil {
		return fmt.Errorf("encode command: %w", err)
	}
	response, err := client.do(ctx, http.MethodPost, commandsPath, body)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return newRemoteHTTPError(response, http.MethodPost, commandsPath)
	}
	return nil
}

func (command remoteSetCommand) confirm(snapshot runtimeStatus) error {
	switch command.target {
	case "readers":
		expected, err := command.numericValue()
		if err != nil {
			return err
		}
		if snapshot.Reader.Workers != expected {
			return fmt.Errorf("verification snapshot reader.workers = %v, want %v", snapshot.Reader.Workers, expected)
		}
	case "senders":
		expected, err := command.numericValue()
		if err != nil {
			return err
		}
		if snapshot.Sender.Workers != expected {
			return fmt.Errorf("verification snapshot sender.workers = %v, want %v", snapshot.Sender.Workers, expected)
		}
	case "tps":
		expected, err := command.numericValue()
		if err != nil {
			return err
		}
		if snapshot.Throttler.RequestedTps != expected {
			return fmt.Errorf("verification snapshot throttler.requestedTps = %v, want %v", snapshot.Throttler.RequestedTps, expected)
		}
	case "throttle":
		expected, ok := command.value.(bool)
		if !ok {
			return fmt.Errorf("verification snapshot throttle value has invalid type")
		}
		if snapshot.Throttler.Installed != expected {
			return fmt.Errorf("verification snapshot throttler.installed = %v, want %v", snapshot.Throttler.Installed, expected)
		}
	default:
		return fmt.Errorf("verification snapshot unknown set target %q", command.target)
	}
	return nil
}

func (command remoteSetCommand) numericValue() (int, error) {
	value, ok := command.value.(int)
	if !ok {
		return 0, fmt.Errorf("verification snapshot %s value has invalid type", command.target)
	}
	return value, nil
}

func (client remoteCLIClient) do(ctx context.Context, method, endpoint string, body []byte) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build %s request: %w", method, err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, endpoint, err)
	}
	return response, nil
}

func newRemoteHTTPError(response *http.Response, method, endpoint string) remoteHTTPError {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
	detail := strings.TrimSpace(string(body))
	return remoteHTTPError{status: response.Status, method: method, endpoint: endpoint, detail: detail}
}

func decodeStrictSnapshot(body io.Reader) (runtimeStatus, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return runtimeStatus{}, fmt.Errorf("read snapshot: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var snapshot runtimeStatus
	if err := decoder.Decode(&snapshot); err != nil {
		return runtimeStatus{}, fmt.Errorf("decode snapshot: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return runtimeStatus{}, fmt.Errorf("decode snapshot: trailing JSON")
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return runtimeStatus{}, fmt.Errorf("decode snapshot shape: %w", err)
	}
	if err := validateSnapshotKeys(root); err != nil {
		return runtimeStatus{}, err
	}
	switch snapshot.Run.State {
	case runStateStarting, runStateRunning, runStateStopping, runStateFaulted:
	default:
		return runtimeStatus{}, fmt.Errorf("decode snapshot: invalid run state %q", snapshot.Run.State)
	}
	return snapshot, nil
}

func validateSnapshotKeys(root map[string]json.RawMessage) error {
	sections := map[string][]string{
		"":              {"config", "reader", "readerChannel", "run", "sender", "senderChannel", "throttler"},
		"run":           {"elapsedMs", "state", "totalTransactions"},
		"reader":        {"blockedWorkers", "drainingBlockedWorkers", "drainingIdleWorkers", "drainingReadingWorkers", "drainingWorkers", "idleWorkers", "liveWorkers", "readBatchSize", "readTps", "readingWorkers", "rowsRead", "sourceDirectory", "sourceError", "workers"},
		"throttler":     {"admittedTps", "installed", "requestedTps"},
		"sender":        {"backoffWorkers", "drainingBackoffWorkers", "drainingIdleWorkers", "drainingInFlightWorkers", "drainingWorkers", "idleWorkers", "inFlightWorkers", "liveWorkers", "workers"},
		"readerChannel": {"blockedMs", "blockedSenders", "bufferedTransactions", "capacity", "depthBatches", "inputBatchesPerSecond", "inputTransactionsPerSecond", "oldestBlockedSenderMs", "outputBatchesPerSecond", "outputTransactionsPerSecond", "receivedBatchesTotal", "receivedTransactionsTotal", "sentBatchesTotal", "sentTransactionsTotal"},
		"senderChannel": {"blockedMs", "blockedSenders", "bufferedTransactions", "capacity", "depthBatches", "inputBatchesPerSecond", "inputTransactionsPerSecond", "oldestBlockedSenderMs", "outputBatchesPerSecond", "outputTransactionsPerSecond", "receivedBatchesTotal", "receivedTransactionsTotal", "sentBatchesTotal", "sentTransactionsTotal"},
		"config":        {"logging", "metricsWindowMs", "readerChannelCapacity", "readerReadBatchSize", "readerWorkers", "senderChannelCapacity", "senderWorkers", "throttlerInstalled", "throttlerRequestedTps"},
	}
	if err := validateExactKeys("snapshot", root, sections[""]); err != nil {
		return err
	}
	for section, expected := range sections {
		if section == "" {
			continue
		}
		var value map[string]json.RawMessage
		if err := json.Unmarshal(root[section], &value); err != nil {
			return fmt.Errorf("decode snapshot %s: %w", section, err)
		}
		if err := validateExactKeys("snapshot "+section, value, expected); err != nil {
			return err
		}
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(root["config"], &config); err != nil {
		return fmt.Errorf("decode snapshot config: %w", err)
	}
	for _, name := range []string{"readerReadBatchSize", "readerWorkers", "metricsWindowMs", "senderWorkers", "throttlerRequestedTps"} {
		if err := validateSnapshotObjectKeys(config, name, []string{"initial", "max", "min", "mutability", "step", "unit"}); err != nil {
			return err
		}
	}
	for _, name := range []string{"readerChannelCapacity", "senderChannelCapacity"} {
		if err := validateSnapshotObjectKeys(config, name, []string{"allowed", "initial", "mutability", "unit"}); err != nil {
			return err
		}
	}
	if err := validateSnapshotObjectKeys(config, "throttlerInstalled", []string{"allowed", "initial", "mutability"}); err != nil {
		return err
	}
	var throttler map[string]json.RawMessage
	if err := json.Unmarshal(root["throttler"], &throttler); err != nil {
		return fmt.Errorf("decode snapshot throttler: %w", err)
	}
	var installedConfig map[string]json.RawMessage
	if err := json.Unmarshal(config["throttlerInstalled"], &installedConfig); err != nil {
		return fmt.Errorf("decode snapshot throttlerInstalled: %w", err)
	}
	for _, raw := range []json.RawMessage{throttler["installed"], installedConfig["initial"]} {
		value := string(bytes.TrimSpace(raw))
		if value != "true" && value != "false" {
			return fmt.Errorf("decode snapshot installed value must be boolean")
		}
	}
	var allowed []json.RawMessage
	if err := json.Unmarshal(installedConfig["allowed"], &allowed); err != nil || allowed == nil {
		return fmt.Errorf("decode snapshot throttlerInstalled.allowed must be boolean array")
	}
	for _, raw := range allowed {
		value := string(bytes.TrimSpace(raw))
		if value != "true" && value != "false" {
			return fmt.Errorf("decode snapshot throttlerInstalled.allowed must contain only booleans")
		}
	}
	if err := validateSnapshotObjectKeys(config, "logging", []string{"level", "mutability"}); err != nil {
		return err
	}
	var reader map[string]json.RawMessage
	if err := json.Unmarshal(root["reader"], &reader); err != nil {
		return fmt.Errorf("decode snapshot reader: %w", err)
	}
	if string(reader["sourceError"]) != "null" {
		var sourceError map[string]json.RawMessage
		if err := json.Unmarshal(reader["sourceError"], &sourceError); err != nil {
			return fmt.Errorf("decode snapshot sourceError: %w", err)
		}
		if err := validateExactKeys("snapshot sourceError", sourceError, []string{"category", "message", "operation", "relativePath"}); err != nil {
			return err
		}
	}
	return nil
}

func validateSnapshotObjectKeys(parent map[string]json.RawMessage, name string, expected []string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(parent[name], &object); err != nil {
		return fmt.Errorf("decode snapshot %s: %w", name, err)
	}
	return validateExactKeys("snapshot "+name, object, expected)
}

func validateExactKeys(name string, object map[string]json.RawMessage, expected []string) error {
	if len(object) != len(expected) {
		return fmt.Errorf("invalid %s schema", name)
	}
	for _, key := range expected {
		if _, ok := object[key]; !ok {
			return fmt.Errorf("invalid %s schema", name)
		}
	}
	return nil
}

func formatRuntimeStatusCard(status runtimeStatus) string {
	sourceDirectory := sanitizeRuntimeStatusValue(status.Reader.SourceDirectory)
	if sourceDirectory == "" {
		sourceDirectory = "none"
	}

	sourceError := "none"
	if status.Reader.SourceError != nil {
		sourceError = fmt.Sprintf(
			"%s/%s %s: %s",
			sanitizeRuntimeStatusValue(status.Reader.SourceError.Category),
			sanitizeRuntimeStatusValue(status.Reader.SourceError.Operation),
			sanitizeRuntimeStatusValue(status.Reader.SourceError.RelativePath),
			sanitizeRuntimeStatusValue(status.Reader.SourceError.Message),
		)
	}

	var card strings.Builder
	fmt.Fprintf(&card, "INGESTION LAB LOADGEN STATUS\n")
	fmt.Fprintf(&card, "State:                    %s\n", sanitizeRuntimeStatusValue(string(status.Run.State)))
	fmt.Fprintf(&card, "Elapsed:                  %s\n", time.Duration(status.Run.ElapsedMs)*time.Millisecond)
	fmt.Fprintf(&card, "Transactions:             %d\n\n", status.Run.TotalTransactions)
	fmt.Fprintf(&card, "Throttler\n")
	fmt.Fprintf(&card, "Requested TPS:            %d\n", status.Throttler.RequestedTps)
	fmt.Fprintf(&card, "Admitted TPS:             %.1f\n", status.Throttler.AdmittedTps)
	fmt.Fprintf(&card, "Installed:                %t\n\n", status.Throttler.Installed)
	fmt.Fprintf(&card, "Reader\n")
	fmt.Fprintf(
		&card,
		"Workers:                  configured=%d live=%d reading=%d idle=%d blocked=%d draining=%d\n",
		status.Reader.Workers,
		status.Reader.LiveWorkers,
		status.Reader.ReadingWorkers,
		status.Reader.IdleWorkers,
		status.Reader.BlockedWorkers,
		status.Reader.DrainingWorkers,
	)
	fmt.Fprintf(&card, "Read TPS:                 %.1f\n", status.Reader.ReadTps)
	fmt.Fprintf(&card, "Rows read:                %d\n", status.Reader.RowsRead)
	fmt.Fprintf(&card, "Source:                   %s\n", sourceDirectory)
	fmt.Fprintf(&card, "Source error:             %s\n\n", sourceError)
	writeRuntimeStatusChannel(&card, "Reader channel", status.ReaderChannel)
	writeRuntimeStatusChannel(&card, "Sender channel", status.SenderChannel)
	fmt.Fprintf(&card, "Sender\n")
	fmt.Fprintf(
		&card,
		"Workers:                  configured=%d live=%d in-flight=%d idle=%d backoff=%d draining=%d\n",
		status.Sender.Workers,
		status.Sender.LiveWorkers,
		status.Sender.InFlightWorkers,
		status.Sender.IdleWorkers,
		status.Sender.BackoffWorkers,
		status.Sender.DrainingWorkers,
	)
	return card.String()
}

func sanitizeRuntimeStatusValue(value string) string {
	var sanitized strings.Builder
	for _, character := range value {
		if !unicode.IsControl(character) {
			sanitized.WriteRune(character)
			continue
		}
		if character <= 0xff {
			fmt.Fprintf(&sanitized, "\\x%02x", character)
			continue
		}
		fmt.Fprintf(&sanitized, "\\u%04x", character)
	}
	return sanitized.String()
}

func writeRuntimeStatusChannel(card *strings.Builder, title string, channel runtimeChannelStatus) {
	fmt.Fprintf(card, "%s\n", title)
	fmt.Fprintf(
		card,
		"Queue:                    %d/%d batches; %d transactions\n",
		channel.DepthBatches,
		channel.Capacity,
		channel.BufferedTransactions,
	)
	fmt.Fprintf(
		card,
		"Input / output TPS:       %.1f / %.1f\n",
		channel.SentTransactionsPerSecond,
		channel.ReceivedTransactionsPerSecond,
	)
	fmt.Fprintf(
		card,
		"Blocked:                  senders=%d oldest=%dms total=%dms\n\n",
		channel.BlockedSenders,
		channel.OldestBlockedSenderMs,
		channel.BlockedMs,
	)
}
