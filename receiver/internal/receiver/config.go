package receiver

import (
	"fmt"
	"math"
	"os"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const currentSchemaVersion = 1

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
	Path         string `toml:"path"`
	MaxBodyBytes int64  `toml:"max_body_bytes"`
}

type LoggingConfig struct {
	Level string `toml:"level"`
}

type LabConfig struct {
	ResponseDelay  time.Duration
	ResponseStatus *int
}

type fileConfig struct {
	SchemaVersion int              `toml:"schema_version"`
	Server        fileServerConfig `toml:"server"`
	Ingest        IngestConfig     `toml:"ingest"`
	Logging       LoggingConfig    `toml:"logging"`
	Lab           fileLabConfig    `toml:"lab"`
}

type fileServerConfig struct {
	Address           string `toml:"address"`
	ShutdownTimeoutMS int64  `toml:"shutdown_timeout_ms"`
}

type fileLabConfig struct {
	ResponseDelayMS int64 `toml:"response_delay_ms"`
	ResponseStatus  *int  `toml:"response_status"`
}

func LoadConfig(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()

	var decoded fileConfig
	decoder := toml.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	config, err := decoded.config()
	if err != nil {
		return Config{}, err
	}
	return config, nil
}

func (config fileConfig) config() (Config, error) {
	shutdownTimeout, err := durationFromMilliseconds(
		config.Server.ShutdownTimeoutMS,
		"server.shutdown_timeout_ms",
	)
	if err != nil {
		return Config{}, err
	}
	responseDelay, err := nonNegativeDurationFromMilliseconds(
		config.Lab.ResponseDelayMS,
		"lab.response_delay_ms",
	)
	if err != nil {
		return Config{}, err
	}
	result := Config{
		SchemaVersion: config.SchemaVersion,
		Server: ServerConfig{
			Address:         config.Server.Address,
			ShutdownTimeout: shutdownTimeout,
		},
		Ingest:  config.Ingest,
		Logging: config.Logging,
		Lab: LabConfig{
			ResponseDelay:  responseDelay,
			ResponseStatus: config.Lab.ResponseStatus,
		},
	}
	if err := validateConfig(result); err != nil {
		return Config{}, err
	}

	return result, nil
}

func durationFromMilliseconds(value int64, field string) (time.Duration, error) {
	if value <= 0 {
		return 0, fmt.Errorf("%s must be positive", field)
	}
	return nonNegativeDurationFromMilliseconds(value, field)
}

func nonNegativeDurationFromMilliseconds(value int64, field string) (time.Duration, error) {
	if value < 0 {
		return 0, fmt.Errorf("%s must not be negative", field)
	}
	if value > math.MaxInt64/int64(time.Millisecond) {
		return 0, fmt.Errorf("%s is too large", field)
	}

	return time.Duration(value) * time.Millisecond, nil
}

func validateConfig(config Config) error {
	if config.SchemaVersion != currentSchemaVersion {
		return fmt.Errorf("schema_version must be %d", currentSchemaVersion)
	}
	if config.Server.Address == "" {
		return fmt.Errorf("server.address must not be empty")
	}
	if config.Server.ShutdownTimeout <= 0 {
		return fmt.Errorf("server.shutdown_timeout_ms must be positive")
	}
	if config.Ingest.Path != "/internal/ingest" {
		return fmt.Errorf("ingest.path must be /internal/ingest")
	}
	if config.Ingest.MaxBodyBytes <= 0 || config.Ingest.MaxBodyBytes == math.MaxInt64 {
		return fmt.Errorf("ingest.max_body_bytes must be positive and bounded")
	}
	if !supportedLoggingLevel(config.Logging.Level) {
		return fmt.Errorf("logging.level is unsupported")
	}
	if config.Lab.ResponseDelay < 0 {
		return fmt.Errorf("lab.response_delay_ms must not be negative")
	}
	if !supportedResponseStatus(config.Lab.responseStatus()) {
		return fmt.Errorf("lab.response_status is unsupported")
	}

	return nil
}

func supportedLoggingLevel(level string) bool {
	switch level {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}

func (config LabConfig) responseStatus() int {
	if config.ResponseStatus == nil || *config.ResponseStatus == 0 {
		return 204
	}
	return *config.ResponseStatus
}

func supportedResponseStatus(status int) bool {
	switch status {
	case 204, 400, 500:
		return true
	default:
		return false
	}
}
