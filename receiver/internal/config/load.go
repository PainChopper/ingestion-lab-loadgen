// Package config loads and validates Receiver settings.
package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"time"

	"github.com/PainChopper/ingestion-lab-loadgen/receiver/internal/ingest"

	"github.com/go-viper/mapstructure/v2"
	"github.com/pelletier/go-toml/v2"
)

// Config contains the settings used to start the Receiver service.
type Config struct {
	// Server configures the listener and graceful shutdown.
	Server serverConfig
	// Ingest configures the batch endpoint and its responses.
	Ingest ingest.Config
	// Logging configures the service logger.
	Logging loggingConfig
}

type serverConfig struct {
	// Address is the listener host and port.
	Address string
	// ShutdownTimeout limits the time to finish active HTTP requests.
	ShutdownTimeout time.Duration
}

type loggingConfig struct {
	// Level sets the minimum log level.
	Level string `mapstructure:"level"`
}

type fileConfig struct {
	// Server contains the listener and shutdown settings from TOML.
	Server fileServerConfig `mapstructure:"server"`
	// Ingest contains the endpoint settings from TOML.
	Ingest fileIngestConfig `mapstructure:"ingest"`
	// Logging contains the logging settings from TOML.
	Logging loggingConfig `mapstructure:"logging"`
	// Lab contains the response settings from TOML.
	Lab fileLabConfig `mapstructure:"lab"`
}

type fileIngestConfig struct {
	// Path is the exact route for receiving batches.
	Path string `mapstructure:"path"`
	// MaxBodyBytes limits the size of each batch request body.
	MaxBodyBytes int64 `mapstructure:"max_body_bytes"`
}

type fileServerConfig struct {
	// Address is the listener host and port.
	Address string `mapstructure:"address"`
	// ShutdownTimeoutMS limits graceful shutdown in milliseconds.
	ShutdownTimeoutMS int64 `mapstructure:"shutdown_timeout_ms"`
}

type fileLabConfig struct {
	// ResponseDelayMS is the delay before responding to a valid batch.
	ResponseDelayMS int64 `mapstructure:"response_delay_ms"`
	// ResponseStatus overrides the default HTTP 204 for valid batches.
	ResponseStatus *int `mapstructure:"response_status"`
}

// Load reads, decodes, and validates the Receiver TOML configuration at path.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var values map[string]any
	if err := toml.Unmarshal(data, &values); err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var decoded fileConfig
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result: &decoded, TagName: "mapstructure", ErrorUnused: true, ErrorUnset: true,
		AllowUnsetPointer: true, WeaklyTypedInput: false, DecodeHook: rejectFractionalInt,
		MatchName: func(mapKey, fieldName string) bool { return mapKey == fieldName },
	})
	if err != nil {
		return Config{}, fmt.Errorf("create config decoder: %w", err)
	}
	if err := decoder.Decode(values); err != nil {
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
		Server: serverConfig{
			Address:         c.Server.Address,
			ShutdownTimeout: shutdownTimeout,
		},
		Logging: c.Logging,
		Ingest: ingest.Config{
			Path:           c.Ingest.Path,
			MaxBodyBytes:   c.Ingest.MaxBodyBytes,
			ResponseDelay:  responseDelay,
			ResponseStatus: c.Lab.ResponseStatus,
		},
	}
	if err := validateConfig(result); err != nil {
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

func validateConfig(config Config) error {
	if config.Server.Address == "" {
		return errors.New("server.address must not be empty")
	}
	if config.Server.ShutdownTimeout <= 0 {
		return errors.New("server.shutdown_timeout_ms must be positive")
	}
	if !supportedLoggingLevel(config.Logging.Level) {
		return errors.New("logging.level is unsupported")
	}
	return config.Ingest.Validate()
}

func supportedLoggingLevel(level string) bool {
	switch level {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}
