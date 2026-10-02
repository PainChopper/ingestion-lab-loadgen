package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultCLIBaseURL   = "http://127.0.0.1:8080"
	remoteClientTimeout = 10 * time.Second

	cliExitSuccess = 0
	cliExitLocal   = 1
	cliExitUsage   = 2
	cliExitHTTP    = 3
	cliExitRemote  = 4
)

type cliUsageError struct{ err error }

func (err cliUsageError) Error() string { return err.err.Error() }

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

func cliExitCode(err error) int {
	var usageErr cliUsageError
	if errors.As(err, &usageErr) {
		return cliExitUsage
	}
	var httpErr remoteHTTPError
	if errors.As(err, &httpErr) {
		return cliExitHTTP
	}
	return cliExitRemote
}

func (command cliCommand) usage() string {
	if command.remoteAction == "set" {
		return cliSetUsage
	}
	if command.remoteAction == "" {
		return cliUsage
	}
	return fmt.Sprintf("usage: ingestion-lab-loadgen %s [--url <url>]", command.remoteAction)
}

const cliSetUsage = `usage:
  ingestion-lab-loadgen set reader-workers <N> [--url <url>]
  ingestion-lab-loadgen set sender-workers <N> [--url <url>]
  ingestion-lab-loadgen set requested-tps <N> [--url <url>]
  ingestion-lab-loadgen set throttler-mode <installed|bypass> [--url <url>]`

type remoteSetCommand struct {
	target        string
	requestAction string
	value         any
}

func isRemoteCLIAction(action string) bool {
	switch action {
	case "snapshot", "status", "run", "pause", "resume", "reset":
		return true
	default:
		return false
	}
}

func parseRemoteCLI(args []string) (cliCommand, error) {
	command := cliCommand{remoteAction: args[0], remoteURL: defaultCLIBaseURL}
	if len(args) == 2 && args[1] == "--help" {
		command.showUsage = true
		return command, nil
	}
	if len(args) == 1 {
		return command, nil
	}
	if len(args) != 3 || args[1] != "--url" || args[2] == "" {
		return command, fmt.Errorf("invalid %s arguments", args[0])
	}
	baseURL, err := validateCLIBaseURL(args[2])
	if err != nil {
		return command, cliUsageError{err: err}
	}
	command.remoteURL = baseURL
	return command, nil
}

func parseSetCLI(args []string) (cliCommand, error) {
	command := cliCommand{remoteAction: "set", remoteURL: defaultCLIBaseURL}
	if len(args) == 2 && args[1] == "--help" {
		command.showUsage = true
		return command, nil
	}
	if len(args) < 2 {
		return command, fmt.Errorf("invalid set arguments")
	}
	setCommand, err := newRemoteSetCommand(args[1], "")
	if err != nil {
		return command, err
	}
	if len(args) == 3 && args[2] == "--help" {
		command.showUsage = true
		return command, nil
	}
	if len(args) != 3 && len(args) != 5 {
		return command, fmt.Errorf("invalid set arguments")
	}
	if args[2] == "" {
		return command, fmt.Errorf("invalid set arguments")
	}
	if len(args) == 5 {
		if args[3] != "--url" || args[4] == "" {
			return command, fmt.Errorf("invalid set arguments")
		}
		baseURL, err := validateCLIBaseURL(args[4])
		if err != nil {
			return command, cliUsageError{err: err}
		}
		command.remoteURL = baseURL
	}
	setCommand, err = newRemoteSetCommand(args[1], args[2])
	if err != nil {
		return command, err
	}
	command.remoteSet = setCommand
	return command, nil
}

func newRemoteSetCommand(target, rawValue string) (remoteSetCommand, error) {
	setCommand := remoteSetCommand{target: target}
	switch target {
	case "reader-workers":
		setCommand.requestAction = "set-reader-workers"
	case "sender-workers":
		setCommand.requestAction = "set-sender-workers"
	case "requested-tps":
		setCommand.requestAction = "set-requested-tps"
	case "throttler-mode":
		setCommand.requestAction = "set-throttler-installation-mode"
		if rawValue == "" {
			return setCommand, nil
		}
		setCommand.value = rawValue
		return setCommand, nil
	default:
		return remoteSetCommand{}, fmt.Errorf("unknown set target %q", target)
	}
	if rawValue == "" {
		return setCommand, nil
	}
	value, err := strconv.Atoi(rawValue)
	if err != nil {
		return remoteSetCommand{}, fmt.Errorf("invalid %s value %q", target, rawValue)
	}
	setCommand.value = value
	return setCommand, nil
}

func validateCLIBaseURL(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return "", fmt.Errorf("--url must be an absolute HTTP(S) URL with host")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("--url must use http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("--url must not include userinfo, query, fragment, or a base path")
	}
	parsed.Path = ""
	return parsed.String(), nil
}

func runRemoteCLI(ctx context.Context, command cliCommand, stdout io.Writer) error {
	client := remoteCLIClient{baseURL: command.remoteURL, httpClient: &http.Client{Timeout: remoteClientTimeout}}
	if command.remoteAction == "snapshot" {
		snapshot, err := client.snapshot(ctx)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			return fmt.Errorf("encode snapshot: %w", err)
		}
		_, err = fmt.Fprintln(stdout, string(encoded))
		return err
	}
	if command.remoteAction == "status" {
		snapshot, err := client.snapshot(ctx)
		if err != nil {
			return err
		}
		_, err = io.WriteString(stdout, formatRuntimeStatusCard(runtimeSummaryFromStatus(snapshot)))
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

	action := command.remoteAction
	requestAction := action
	if action == "resume" {
		requestAction = "run"
	}
	if err := client.command(ctx, requestAction); err != nil {
		return err
	}
	snapshot, err := client.snapshot(ctx)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "action=%s state=%s\n", action, snapshot.Run.State)
	return err
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

func (client remoteCLIClient) command(ctx context.Context, action string) error {
	return client.commandWithValue(ctx, action, nil)
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
	case "reader-workers":
		expected, err := command.numericValue()
		if err != nil {
			return err
		}
		if snapshot.Reader.Workers != expected {
			return fmt.Errorf("verification snapshot reader.workers = %v, want %v", snapshot.Reader.Workers, expected)
		}
	case "sender-workers":
		expected, err := command.numericValue()
		if err != nil {
			return err
		}
		if snapshot.Sender.Workers != expected {
			return fmt.Errorf("verification snapshot sender.workers = %v, want %v", snapshot.Sender.Workers, expected)
		}
	case "requested-tps":
		expected, err := command.numericValue()
		if err != nil {
			return err
		}
		if snapshot.Throttler.RequestedTps != expected {
			return fmt.Errorf("verification snapshot throttler.requestedTps = %v, want %v", snapshot.Throttler.RequestedTps, expected)
		}
	case "throttler-mode":
		expected, ok := command.value.(string)
		if !ok {
			return fmt.Errorf("verification snapshot throttler-mode value has invalid type")
		}
		if string(snapshot.Throttler.InstallationMode) != expected {
			return fmt.Errorf("verification snapshot throttler.installationMode = %v, want %v", snapshot.Throttler.InstallationMode, expected)
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
	return snapshot, nil
}

func validateSnapshotKeys(root map[string]json.RawMessage) error {
	sections := map[string][]string{
		"":              {"config", "reader", "readerChannel", "run", "sender", "senderChannel", "throttler"},
		"run":           {"elapsedMs", "state", "totalTransactions"},
		"reader":        {"blockedWorkers", "drainingBlockedWorkers", "drainingIdleWorkers", "drainingReadingWorkers", "drainingWorkers", "idleWorkers", "liveWorkers", "readBatchSize", "readTps", "readingWorkers", "rowsRead", "sourceDirectory", "sourceError", "workers"},
		"throttler":     {"admittedTps", "installationMode", "requestedTps"},
		"sender":        {"backoffWorkers", "drainingBackoffWorkers", "drainingIdleWorkers", "drainingInFlightWorkers", "drainingWorkers", "idleWorkers", "inFlightWorkers", "liveWorkers", "workers"},
		"readerChannel": {"blockedMs", "blockedSenders", "bufferedTransactions", "capacity", "depthBatches", "inputBatchesPerSecond", "inputTransactionsPerSecond", "oldestBlockedSenderMs", "outputBatchesPerSecond", "outputTransactionsPerSecond", "receivedBatchesTotal", "receivedTransactionsTotal", "sentBatchesTotal", "sentTransactionsTotal"},
		"senderChannel": {"blockedMs", "blockedSenders", "bufferedTransactions", "capacity", "depthBatches", "inputBatchesPerSecond", "inputTransactionsPerSecond", "oldestBlockedSenderMs", "outputBatchesPerSecond", "outputTransactionsPerSecond", "receivedBatchesTotal", "receivedTransactionsTotal", "sentBatchesTotal", "sentTransactionsTotal"},
		"config":        {"logging", "metricsWindowMs", "readerChannelCapacity", "readerReadBatchSize", "readerWorkers", "senderChannelCapacity", "senderWorkers", "throttlerInstallationMode", "throttlerRequestedTps"},
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
	if err := validateSnapshotObjectKeys(config, "throttlerInstallationMode", []string{"allowed", "initial", "mutability"}); err != nil {
		return err
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
