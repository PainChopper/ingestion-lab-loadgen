package receiver

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"path"
	"reflect"
	"strings"
	"time"
	"unicode"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

const currentSchemaVersion = 2

type Config struct {
	SchemaVersion int
	Server        ServerConfig
	Ingest        IngestConfig
	Logging       LoggingConfig
	Lab           LabConfig
}

type ServerConfig struct {
	Address         string
	ShutdownTimeout time.Duration
}

type IngestConfig struct {
	Path         string `mapstructure:"path"`
	MaxBodyBytes int64  `mapstructure:"max_body_bytes"`
}

type LoggingConfig struct {
	Level string `mapstructure:"level"`
}

type LabConfig struct {
	ResponseDelay  time.Duration
	ResponseStatus *int
}

type fileConfig struct {
	SchemaVersion int              `mapstructure:"schema_version"`
	Server        fileServerConfig `mapstructure:"server"`
	Ingest        IngestConfig     `mapstructure:"ingest"`
	Logging       LoggingConfig    `mapstructure:"logging"`
	Lab           fileLabConfig    `mapstructure:"lab"`
}

type fileServerConfig struct {
	Address           string `mapstructure:"address"`
	ShutdownTimeoutMS int64  `mapstructure:"shutdown_timeout_ms"`
}

type fileLabConfig struct {
	ResponseDelayMS int64 `mapstructure:"response_delay_ms"`
	ResponseStatus  *int  `mapstructure:"response_status"`
}

func LoadConfig(path string) (Config, error) {
	decoder := viper.New()
	decoder.SetConfigFile(path)
	if err := decoder.ReadInConfig(); err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var decoded fileConfig
	if err := decoder.UnmarshalExact(&decoded, func(options *mapstructure.DecoderConfig) {
		options.ErrorUnset = true
		options.AllowUnsetPointer = true
		options.WeaklyTypedInput = false
		options.DecodeHook = rejectFractionalInt
	}); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	config, err := decoded.config()
	if err != nil {
		return Config{}, err
	}
	return config, nil
}

func (c fileConfig) config() (Config, error) {
	shutdownTimeout, err := durationFromMilliseconds(
		c.Server.ShutdownTimeoutMS,
		"server.shutdown_timeout_ms",
	)
	if err != nil {
		return Config{}, err
	}
	responseDelay, err := durationFromMilliseconds(
		c.Lab.ResponseDelayMS,
		"lab.response_delay_ms",
	)
	if err != nil {
		return Config{}, err
	}
	result := Config{
		SchemaVersion: c.SchemaVersion,
		Server: ServerConfig{
			Address:         c.Server.Address,
			ShutdownTimeout: shutdownTimeout,
		},
		Ingest:  c.Ingest,
		Logging: c.Logging,
		Lab: LabConfig{
			ResponseDelay:  responseDelay,
			ResponseStatus: c.Lab.ResponseStatus,
		},
	}
	if _, err := validateConfig(result); err != nil {
		return Config{}, err
	}

	return result, nil
}

func durationFromMilliseconds(value int64, field string) (time.Duration, error) {
	if value > math.MaxInt64/int64(time.Millisecond) || value < math.MinInt64/int64(time.Millisecond) {
		return 0, fmt.Errorf("%s is too large", field)
	}
	return time.Duration(value) * time.Millisecond, nil
}

func rejectFractionalInt(from, to reflect.Type, data any) (any, error) {
	if from.Kind() != reflect.Float32 && from.Kind() != reflect.Float64 {
		return data, nil
	}
	switch to.Kind() {
	case reflect.Int, reflect.Int64:
	default:
		return data, nil
	}
	value := reflect.ValueOf(data).Float()
	if math.Trunc(value) != value || math.IsInf(value, 0) {
		return nil, errors.New("must be an integer")
	}
	limit := math.Ldexp(1, to.Bits()-1)
	if value < -limit || value >= limit {
		return nil, errors.New("integer is out of range")
	}
	return data, nil
}

func validateConfig(config Config) (string, error) {
	if config.SchemaVersion != currentSchemaVersion {
		return "", fmt.Errorf("schema_version must be %d", currentSchemaVersion)
	}
	if config.Server.Address == "" {
		return "", errors.New("server.address must not be empty")
	}
	if config.Server.ShutdownTimeout <= 0 {
		return "", errors.New("server.shutdown_timeout_ms must be positive")
	}
	pattern, err := ingestPattern(config.Ingest.Path)
	if err != nil {
		return "", fmt.Errorf("ingest.path: %w", err)
	}
	if config.Ingest.MaxBodyBytes <= 0 || config.Ingest.MaxBodyBytes == math.MaxInt64 {
		return "", errors.New("ingest.max_body_bytes must be positive and bounded")
	}
	if !supportedLoggingLevel(config.Logging.Level) {
		return "", errors.New("logging.level is unsupported")
	}
	if config.Lab.ResponseDelay < 0 {
		return "", errors.New("lab.response_delay_ms must not be negative")
	}
	if !supportedResponseStatus(config.Lab.responseStatus()) {
		return "", errors.New("lab.response_status is unsupported")
	}

	return pattern, nil
}

func supportedLoggingLevel(level string) bool {
	switch level {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}

func (c LabConfig) responseStatus() int {
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
