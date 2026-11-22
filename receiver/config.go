package main

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

type Config struct {
	Server  ServerConfig
	Ingest  ingest.Config
	Logging LoggingConfig
}

type ServerConfig struct {
	Address         string
	ShutdownTimeout time.Duration
}

type LoggingConfig struct {
	Level string `mapstructure:"level"`
}

type fileConfig struct {
	Server  fileServerConfig `mapstructure:"server"`
	Ingest  fileIngestConfig `mapstructure:"ingest"`
	Logging LoggingConfig    `mapstructure:"logging"`
	Lab     fileLabConfig    `mapstructure:"lab"`
}

type fileIngestConfig struct {
	Path         string `mapstructure:"path"`
	MaxBodyBytes int64  `mapstructure:"max_body_bytes"`
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
		Server: ServerConfig{
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
