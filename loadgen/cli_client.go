package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	switch command.remoteAction {
	case "status":
	case "set":
		if err := client.commandWithValue(ctx, command.remoteSet.requestAction, command.remoteSet.value); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown remote action %q", command.remoteAction)
	}
	data, err := client.snapshotData(ctx)
	if err != nil {
		return err
	}
	snapshot, err := decodeStrictSnapshot(bytes.NewReader(data))
	if err != nil {
		return err
	}
	if command.remoteAction == "set" {
		if err := command.remoteSet.confirm(data); err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "action=set target=%s value=%v state=%s\n", command.remoteSet.target, command.remoteSet.value, snapshot.Run.State)
		return err
	}
	_, err = io.WriteString(stdout, formatSnapshot(snapshot))
	return err
}

type remoteCLIClient struct {
	baseURL    string
	httpClient *http.Client
}

func (c remoteCLIClient) snapshotData(ctx context.Context) ([]byte, error) {
	response, err := c.do(ctx, http.MethodGet, snapshotPath, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, newRemoteHTTPError(response, http.MethodGet, snapshotPath)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	return data, nil
}

func (c remoteCLIClient) commandWithValue(ctx context.Context, action string, value any) error {
	body, err := json.Marshal(struct {
		Action string `json:"action"`
		Value  any    `json:"value,omitempty"`
	}{Action: action, Value: value})
	if err != nil {
		return fmt.Errorf("encode command: %w", err)
	}
	response, err := c.do(ctx, http.MethodPost, commandsPath, body)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return newRemoteHTTPError(response, http.MethodPost, commandsPath)
	}
	return nil
}

type remoteSetSnapshot struct {
	Reader struct {
		Workers *int `json:"workers"`
	} `json:"reader"`
	Sender struct {
		Workers *int `json:"workers"`
	} `json:"sender"`
	Throttler struct {
		RequestedTps *int  `json:"requestedTps"`
		Installed    *bool `json:"installed"`
	} `json:"throttler"`
}

func (c remoteSetCommand) confirm(data []byte) error {
	// The shared DTO validates the response; pointers distinguish explicit zero from absence/null.
	var snapshot remoteSetSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("decode verification snapshot: %w", err)
	}
	actual := c.actual(snapshot)
	if actual == nil {
		return fmt.Errorf("verification snapshot %s is missing or null", c.field)
	}
	if actual != c.value {
		return fmt.Errorf("verification snapshot %s = %v, want %v", c.field, actual, c.value)
	}
	return nil
}

func (c remoteCLIClient) do(ctx context.Context, method, endpoint string, body []byte) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build %s request: %w", method, err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient.Do(request)
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

func decodeStrictSnapshot(body io.Reader) (runtimeSnapshot, error) {
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	var snapshot runtimeSnapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return runtimeSnapshot{}, fmt.Errorf("decode snapshot: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return runtimeSnapshot{}, fmt.Errorf("decode snapshot: trailing JSON")
	}
	switch snapshot.Run.State {
	case runStateStarting, runStateRunning, runStateStopping, runStateFaulted:
	default:
		return runtimeSnapshot{}, fmt.Errorf("decode snapshot: invalid run state %q", snapshot.Run.State)
	}
	return snapshot, nil
}

func formatSnapshot(snapshot runtimeSnapshot) string {
	reader := countReaderWorkers(snapshot.Reader.WorkerStates)
	sender := countSenderWorkers(snapshot.Sender.WorkerStates)
	sourcePath := sanitizeSnapshotValue(snapshot.Config.SourcePath)
	if sourcePath == "" {
		sourcePath = "none"
	}

	sourceError := "none"
	if snapshot.Reader.SourceError != nil {
		sourceError = fmt.Sprintf(
			"%s %s: %s",
			sanitizeSnapshotValue(snapshot.Reader.SourceError.Operation),
			sanitizeSnapshotValue(snapshot.Reader.SourceError.Path),
			sanitizeSnapshotValue(snapshot.Reader.SourceError.Message),
		)
	}

	var output strings.Builder
	fmt.Fprintf(&output, "INGESTION LAB LOADGEN STATUS\n")
	fmt.Fprintf(&output, "State:                    %s\n", sanitizeSnapshotValue(string(snapshot.Run.State)))
	fmt.Fprintf(&output, "Elapsed:                  %s\n", time.Duration(snapshot.Run.ElapsedMs)*time.Millisecond)
	fmt.Fprintf(&output, "Transactions:             %d\n\n", snapshot.Run.TotalTransactions)
	fmt.Fprintf(&output, "Throttler\n")
	fmt.Fprintf(&output, "Requested TPS:            %d\n", snapshot.Throttler.RequestedTps)
	fmt.Fprintf(&output, "Average admitted TPS:     %.1f\n", averageTPS(snapshot.SenderChannel.InputTransactionsTotal, snapshot.Run.ElapsedMs))
	fmt.Fprintf(&output, "Installed:                %t\n\n", snapshot.Throttler.Installed)
	fmt.Fprintf(&output, "Reader\n")
	fmt.Fprintf(
		&output,
		"Workers:                  configured=%d live=%d reading=%d idle=%d blocked=%d draining=%d\n",
		snapshot.Reader.Workers,
		reader.LiveWorkers,
		reader.ReadingWorkers,
		reader.IdleWorkers,
		reader.BlockedWorkers,
		reader.DrainingWorkers,
	)
	fmt.Fprintf(&output, "Average read TPS:         %.1f\n", averageTPS(snapshot.Reader.RowsRead, snapshot.Run.ElapsedMs))
	fmt.Fprintf(&output, "Rows read:                %d\n", snapshot.Reader.RowsRead)
	fmt.Fprintf(&output, "Source:                   %s\n", sourcePath)
	fmt.Fprintf(&output, "Source error:             %s\n\n", sourceError)
	writeSnapshotChannel(
		&output,
		"Reader channel",
		snapshot.ReaderChannel,
		snapshot.Config.ReaderChannelCapacity,
		snapshot.Config.ReadBatchSize,
		snapshot.Run.ElapsedMs,
	)
	writeSnapshotChannel(
		&output,
		"Sender channel",
		snapshot.SenderChannel,
		snapshot.Config.SenderChannelCapacity,
		snapshot.Config.ReadBatchSize,
		snapshot.Run.ElapsedMs,
	)
	fmt.Fprintf(&output, "Sender\n")
	fmt.Fprintf(
		&output,
		"Workers:                  configured=%d live=%d in-flight=%d idle=%d backoff=%d draining=%d\n",
		snapshot.Sender.Workers,
		sender.LiveWorkers,
		sender.InFlightWorkers,
		sender.IdleWorkers,
		sender.BackoffWorkers,
		sender.DrainingWorkers,
	)
	return output.String()
}

func sanitizeSnapshotValue(value string) string {
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

func writeSnapshotChannel(
	output *strings.Builder,
	title string,
	channel channelSnapshot,
	capacity int,
	batchSize int,
	elapsedMs int64,
) {
	// Validated capacity * batchSize fits Go int and exact JSON integers; depth <= capacity.
	fmt.Fprintf(output, "%s\n", title)
	fmt.Fprintf(
		output,
		"Queue:                    %d/%d batches; approximately %d transactions\n",
		channel.DepthBatches,
		capacity,
		channel.DepthBatches*batchSize,
	)
	fmt.Fprintf(
		output,
		"Average input/output TPS: %.1f / %.1f\n",
		averageTPS(channel.InputTransactionsTotal, elapsedMs),
		averageTPS(channel.OutputTransactionsTotal, elapsedMs),
	)
	fmt.Fprintf(
		output,
		"Blocked:                  senders=%d oldest=%dms total=%dms\n\n",
		channel.BlockedSenders,
		channel.OldestBlockedSenderMs,
		channel.BlockedMs,
	)
}

func averageTPS(total, elapsedMs int64) float64 {
	if elapsedMs <= 0 {
		return 0
	}
	return float64(total) * 1000 / float64(elapsedMs)
}
