package ingest

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode"
)

// Config contains the settings used to receive batches.
type Config struct {
	Path           string
	MaxBodyBytes   int64
	ResponseDelay  time.Duration
	ResponseStatus *int
}

// Validate checks the reception settings without creating a handler.
func (c Config) Validate() error {
	_, err := validateConfig(c)
	return err
}

func validateConfig(config Config) (string, error) {
	pattern, err := ingestPattern(config.Path)
	if err != nil {
		return "", fmt.Errorf("ingest.path: %w", err)
	}
	if config.MaxBodyBytes <= 0 || config.MaxBodyBytes == math.MaxInt64 {
		return "", errors.New("ingest.max_body_bytes must be positive and bounded")
	}
	if config.ResponseDelay < 0 {
		return "", errors.New("lab.response_delay_ms must not be negative")
	}
	if !supportedResponseStatus(config.responseStatus()) {
		return "", errors.New("lab.response_status is unsupported")
	}

	return pattern, nil
}

func (c Config) responseStatus() int {
	if c.ResponseStatus == nil {
		return 204
	}
	return *c.ResponseStatus
}

func supportedResponseStatus(status int) bool {
	switch status {
	case 204, 400, 500:
		return true
	default:
		return false
	}
}

func ingestPattern(raw string) (string, error) {
	invalidRune := func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }
	if !strings.HasPrefix(raw, "/") || raw == "/" || strings.HasSuffix(raw, "/") ||
		path.Clean(raw) != raw || strings.ContainsAny(raw, "{}?#") || strings.IndexFunc(raw, invalidRune) >= 0 {
		return "", errors.New("must be a clean exact path without patterns, query, fragment, or trailing slash")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host != "" || parsed.Scheme != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("must be a valid URL path")
	}
	segments := strings.Split(strings.TrimPrefix(raw, "/"), "/")
	for index, segment := range segments {
		decoded, err := url.PathUnescape(segment)
		if err != nil {
			return "", fmt.Errorf("invalid escaped segment: %w", err)
		}
		if decoded == "." || decoded == ".." || strings.ContainsAny(decoded, "{}") || strings.IndexFunc(decoded, invalidRune) >= 0 {
			return "", errors.New("must not contain dot segments, whitespace, or patterns")
		}
		if len(segments) == 1 && decoded == "status" {
			return "", errors.New("must not use the status endpoint")
		}
		segments[index] = url.PathEscape(decoded)
	}
	return "/" + strings.Join(segments, "/"), nil
}
