package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const maxSenderResponseDrainBytes = 64 << 10

func newSenderHTTPClient(maxWorkers int, timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = maxWorkers
	transport.MaxIdleConnsPerHost = maxWorkers
	transport.MaxConnsPerHost = maxWorkers
	return &http.Client{Transport: transport, Timeout: timeout}
}

type senderClient struct {
	url    string
	client *http.Client
}

func newSenderClient(url string, client *http.Client) senderClient {
	return senderClient{url: url, client: client}
}

func (c senderClient) deliver(ctx context.Context, batch []Transaction, _ int) error {
	body, err := json.Marshal(batch)
	if err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, maxSenderResponseDrainBytes)); err != nil {
		return err
	}

	if response.StatusCode == http.StatusNoContent {
		return nil
	}
	return fmt.Errorf("HTTP status %s", response.Status)
}
