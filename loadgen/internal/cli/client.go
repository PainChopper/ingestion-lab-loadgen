package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"
)

type remoteHTTPError struct {
	status   string
	method   string
	endpoint string
	detail   string
}

// Error reports the failed HTTP request and any response detail.
func (err remoteHTTPError) Error() string {
	message := fmt.Sprintf("HTTP %s %s %s", err.status, err.method, err.endpoint)
	if err.detail != "" {
		return message + ": " + err.detail
	}
	return message
}

func runRemoteCLI(ctx context.Context, command cliCommand, stdout io.Writer) error {
	client := remoteCLIClient{baseURL: command.remoteURL, httpClient: &http.Client{Timeout: remoteClientTimeout}}
	var config api.ConfigResponse
	switch command.remoteAction {
	case "status":
		data, err := client.getData(ctx, api.ConfigPath)
		if err != nil {
			return err
		}
		config, err = api.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("GET %s: %w", api.ConfigPath, restoreJSONTypeDiagnostic(err))
		}
	case "set":
		if err := client.commandWithValue(ctx, command.remoteSet.requestAction, command.remoteSet.value); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown remote action %q", command.remoteAction)
	}
	data, err := client.getData(ctx, api.SnapshotPath)
	if err != nil {
		return err
	}
	snapshot, err := api.DecodeSnapshot(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("GET %s: %w", api.SnapshotPath, restoreJSONTypeDiagnostic(err))
	}
	if command.remoteAction == "set" {
		if err := command.remoteSet.confirm(data); err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "action=set target=%s value=%v state=%s\n", command.remoteSet.target, command.remoteSet.value, snapshot.Run.State)
		return err
	}
	_, err = io.WriteString(stdout, formatSnapshot(snapshot, config))
	return err
}

type remoteCLIClient struct {
	baseURL    string
	httpClient *http.Client
}

func (c remoteCLIClient) getData(ctx context.Context, endpoint string) ([]byte, error) {
	response, err := c.do(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	// The read and status checks report the result; closing releases the body.
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, newRemoteHTTPError(response, http.MethodGet, endpoint)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read GET %s: %w", endpoint, err)
	}
	return data, nil
}

func (c remoteCLIClient) commandWithValue(ctx context.Context, action string, value any) error {
	encodedValue, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode command: %w", err)
	}
	body, err := json.Marshal(api.CommandRequest{Action: action, Value: encodedValue})
	if err != nil {
		return fmt.Errorf("encode command: %w", err)
	}
	response, err := c.do(ctx, http.MethodPost, api.CommandsPath, body)
	if err != nil {
		return err
	}
	// The command outcome comes from the HTTP status.
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return newRemoteHTTPError(response, http.MethodPost, api.CommandsPath)
	}
	return nil
}

// A command confirmation must distinguish a missing value from zero or false.
type remoteSetSnapshot struct {
	// Reader holds the optionally present Reader worker count.
	Reader struct {
		// Workers is the number of configured Reader workers.
		Workers *int `json:"workers"`
	} `json:"reader"`
	// Sender holds the optionally present Sender worker count.
	Sender struct {
		// Workers is the number of configured Sender workers.
		Workers *int `json:"workers"`
	} `json:"sender"`
	// Throttler holds the optionally present pacing settings.
	Throttler struct {
		// RequestedTps is the target transaction rate.
		RequestedTps *int `json:"requestedTps"`
		// Installed is the pacing mode.
		Installed *bool `json:"installed"`
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
		return nil, fmt.Errorf("build %s %s request: %w", method, endpoint, err)
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
