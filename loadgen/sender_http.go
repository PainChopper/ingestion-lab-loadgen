package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
)

type senderHTTPAttempt struct {
	url    string
	client *http.Client
}

func newSenderHTTPAttempt(url string, client *http.Client) senderHTTPAttempt {
	return senderHTTPAttempt{url: url, client: client}
}

func (a senderHTTPAttempt) deliver(ctx context.Context, batch []Transaction, _ int) senderAttemptOutcome {
	body, err := json.Marshal(batch)
	if err != nil {
		return senderAttemptFailure
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url, bytes.NewReader(body))
	if err != nil {
		return senderAttemptFailure
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := a.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return senderAttemptCanceled
		}
		return senderAttemptFailure
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		return senderAttemptFailure
	}

	if response.StatusCode == http.StatusNoContent {
		return senderAttemptSuccess
	}
	return senderAttemptFailure
}
