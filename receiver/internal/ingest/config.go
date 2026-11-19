package ingest

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

var ingestPathPattern = regexp.MustCompile(`^(/[A-Za-z0-9._~-]+)+$`)

// Config contains the settings used to receive batches.
type Config struct {
	Path           string
	MaxBodyBytes   int64
	ResponseDelay  time.Duration
	ResponseStatus *int
}

// Validate checks the reception settings without creating a handler.
func (c Config) Validate() error {
	if err := validateIngestPath(c.Path); err != nil {
		return fmt.Errorf("ingest.path: %w", err)
	}
	if c.MaxBodyBytes <= 0 || c.MaxBodyBytes == math.MaxInt64 {
		return errors.New("ingest.max_body_bytes must be positive and bounded")
	}
	if c.ResponseDelay < 0 {
		return errors.New("lab.response_delay_ms must not be negative")
	}
	if !supportedResponseStatus(c.responseStatus()) {
		return errors.New("lab.response_status is unsupported")
	}

	return nil
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

func validateIngestPath(path string) error {
	if !ingestPathPattern.MatchString(path) {
		return errors.New("must contain only nonempty ASCII segments using letters, digits, '.', '_', '~', or '-'")
	}
	if path == "/status" {
		return errors.New("must not use the status endpoint")
	}
	for segment := range strings.SplitSeq(path[1:], "/") {
		if segment == "." || segment == ".." {
			return errors.New("must not contain dot segments")
		}
	}
	return nil
}
