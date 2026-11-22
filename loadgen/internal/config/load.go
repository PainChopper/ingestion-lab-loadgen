// Package config loads and validates the LoadGen TOML configuration.
package config

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/go-viper/mapstructure/v2"
	"github.com/pelletier/go-toml/v2"
)

const (
	defaultConfigPath = "config.toml"
	// Interoperable exact JSON integers: https://www.rfc-editor.org/rfc/rfc8259#section-6
	maxExactJSONInteger = 1<<53 - 1
)

// Config is the validated LoadGen startup configuration.
type Config struct {
	// HTTP sets the control server's listen address.
	HTTP httpConfig `mapstructure:"http"`
	// Source selects the input Parquet files.
	Source sourceConfig `mapstructure:"source"`
	// Reader configures Parquet reading.
	Reader readerConfig `mapstructure:"reader"`
	// ReaderChannel configures the queue after the Reader.
	ReaderChannel channelConfig `mapstructure:"readerChannel"`
	// SenderChannel configures the queue before the Sender.
	SenderChannel channelConfig `mapstructure:"senderChannel"`
	// Throttler configures transaction pacing.
	Throttler throttlerConfig `mapstructure:"throttler"`
	// Sender configures HTTP delivery.
	Sender senderConfig `mapstructure:"sender"`
	// Logging configures the service logger.
	Logging loggingConfig `mapstructure:"logging"`
}

type httpConfig struct {
	// Addr is the listener host and port.
	Addr string `mapstructure:"addr"`
}

type sourceConfig struct {
	// Path is the input Parquet path or glob.
	Path string `mapstructure:"path"`
}

type readerConfig struct {
	// ReadBatchSize limits the number of transactions in each batch.
	ReadBatchSize int `mapstructure:"read_batch_size"`
	// Workers sets the initial and maximum worker counts.
	Workers workersConfig `mapstructure:"workers"`
}

type channelConfig struct {
	// Capacity sets the queue capacity in batches.
	Capacity int `mapstructure:"capacity"`
}

type throttlerConfig struct {
	// RequestedTPS sets the initial and maximum transaction rate.
	RequestedTPS tpsConfig `mapstructure:"requested_tps"`
	// Installed enables pacing when true.
	Installed bool `mapstructure:"installed"`
}

type loggingConfig struct {
	// Level sets the minimum log level.
	Level string `mapstructure:"level"`
}

type senderConfig struct {
	// Workers sets the initial and maximum worker counts.
	Workers workersConfig `mapstructure:"workers"`
	// API configures the HTTP Receiver endpoint.
	API senderAPIConfig `mapstructure:"api"`
	// Retry configures retry delays.
	Retry senderRetryConfig `mapstructure:"retry"`
}

type senderAPIConfig struct {
	// URL is the Receiver ingestion URL.
	URL string `mapstructure:"url"`
	// TimeoutMS is the timeout for a delivery attempt in milliseconds.
	TimeoutMS int64 `mapstructure:"timeout_ms"`
}

type senderRetryConfig struct {
	// DelaysMS lists the retry delays in milliseconds.
	DelaysMS []int `mapstructure:"delays_ms"`
	// JitterPercent sets the maximum delay jitter as a percentage.
	JitterPercent int `mapstructure:"jitter_percent"`
}

type workersConfig struct {
	// Initial is the startup value.
	Initial int `mapstructure:"initial"`
	// Max is the inclusive upper bound.
	Max int `mapstructure:"max"`
}

type tpsConfig struct {
	// Initial is the startup value.
	Initial int `mapstructure:"initial"`
	// Max is the inclusive upper bound.
	Max int `mapstructure:"max"`
}

// Load reads and validates the TOML file. An empty path selects config.toml.
func Load(configPath string) (Config, error) {
	path := defaultConfigPath
	if configPath != "" {
		path = configPath
	}

	absConfigPath, err := filepath.Abs(path)
	if err != nil {
		return Config{}, fmt.Errorf("make config path absolute: %w", err)
	}

	data, err := os.ReadFile(absConfigPath)
	if err != nil {
		return Config{}, fmt.Errorf("read config %q: %w", absConfigPath, err)
	}
	return decodeConfig(data, absConfigPath)
}

func decodeConfig(data []byte, absConfigPath string) (Config, error) {
	// Decode the complete TOML tree: flattened settings discard empty tables,
	// which would otherwise let unknown sections bypass strict decoding.
	var values map[string]any
	if err := toml.Unmarshal(data, &values); err != nil {
		return Config{}, fmt.Errorf("read config %q: %w", absConfigPath, err)
	}
	var cfg Config
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result: &cfg, TagName: "mapstructure", ErrorUnused: true, ErrorUnset: true,
		WeaklyTypedInput: false, DecodeHook: rejectFractionalInt,
		MatchName: func(mapKey, fieldName string) bool { return mapKey == fieldName },
	})
	if err != nil {
		return Config{}, fmt.Errorf("create config decoder: %w", err)
	}
	if err := decoder.Decode(values); err != nil {
		if http, ok := values["http"].(map[string]any); !ok || http["addr"] == nil {
			err = errors.Join(err, errors.New("http.addr is required"))
		}
		return Config{}, fmt.Errorf("decode config %q: %w", absConfigPath, err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("validate config %q: %w", absConfigPath, err)
	}

	return cfg, nil
}

func rejectFractionalInt(from, to reflect.Type, data any) (any, error) {
	if from.Kind() != reflect.Float32 && from.Kind() != reflect.Float64 {
		return data, nil
	}
	switch to.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
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

func (c Config) validate() error {
	if err := c.HTTP.validate(); err != nil {
		return fmt.Errorf("http.addr: %w", err)
	}
	if c.Source.Path == "" {
		return errors.New("source.path must not be empty")
	}
	if c.Reader.ReadBatchSize <= 0 || int64(c.Reader.ReadBatchSize) > math.MaxInt64/int64(time.Second) {
		return errors.New("reader.read_batch_size must be positive and within the pacing duration limit")
	}
	if err := c.Reader.Workers.validateWorkers(); err != nil {
		return fmt.Errorf("reader.workers: %w", err)
	}
	maxCapacity := min(
		int64(math.MaxInt)/int64(unsafe.Sizeof([]struct{}{})),
		maxExactJSONInteger,
	)
	for _, queue := range []struct {
		name     string
		capacity int
	}{
		{name: "readerChannel.capacity", capacity: c.ReaderChannel.Capacity},
		{name: "senderChannel.capacity", capacity: c.SenderChannel.Capacity},
	} {
		if queue.capacity < 0 || int64(queue.capacity) > maxCapacity {
			return fmt.Errorf("%s must be non-negative, fit its backing array and not exceed %d", queue.name, maxExactJSONInteger)
		}
	}
	if err := c.Throttler.RequestedTPS.validateRequestedTPS(); err != nil {
		return fmt.Errorf("throttler.requested_tps: %w", err)
	}
	if err := c.Sender.validate(); err != nil {
		return fmt.Errorf("sender: %w", err)
	}
	if err := c.Logging.validate(); err != nil {
		return fmt.Errorf("logging: %w", err)
	}
	return nil
}

func (c httpConfig) validate() error {
	host, rawPort, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return fmt.Errorf("must be host:port: %w", err)
	}
	if host == "" || strings.ContainsAny(host, " \t\r\n/\\?#") {
		return errors.New("host must be non-empty and contain no whitespace or URL path")
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || strings.Trim(rawPort, "0123456789") != "" || port < 1 || port > 65535 {
		return errors.New("port must be numeric and between 1 and 65535")
	}
	return nil
}

func (c loggingConfig) validate() error {
	if !slices.Contains([]string{"debug", "info", "warn", "error"}, c.Level) {
		return errors.New("level must be debug, info, warn, or error")
	}
	return nil
}

func (c senderConfig) validate() error {
	if err := c.Workers.validateWorkers(); err != nil {
		return fmt.Errorf("workers: %w", err)
	}
	if err := c.API.validate(); err != nil {
		return fmt.Errorf("api: %w", err)
	}
	return c.Retry.validate()
}

func (c senderRetryConfig) validate() error {
	if c.JitterPercent < 0 || c.JitterPercent > 50 {
		return errors.New("jitter_percent must be between 0 and 50")
	}
	if len(c.DelaysMS) == 0 {
		return errors.New("delays_ms must not be empty")
	}
	for index, delay := range c.DelaysMS {
		if delay <= 0 {
			return fmt.Errorf("delays_ms[%d] must be positive", index)
		}
		if int64(delay) > senderRetryMaxDelayMS(c.JitterPercent) {
			return fmt.Errorf("delays_ms[%d] exceeds the maximum safe retry duration", index)
		}
		if index > 0 && delay < c.DelaysMS[index-1] {
			return errors.New("delays_ms must be nondecreasing")
		}
	}
	return nil
}

func senderRetryMaxDelayMS(jitterPercent int) int64 {
	factor := int64(time.Millisecond)
	if jitterPercent > 0 {
		factor += factor * int64(jitterPercent) / 100
	}
	return math.MaxInt64 / factor
}

func (c senderAPIConfig) validate() error {
	if c.URL == "" {
		return errors.New("url must not be empty")
	}
	const maxTimeoutMS = math.MaxInt64 / int64(time.Millisecond)
	if c.TimeoutMS < 1 || c.TimeoutMS > maxTimeoutMS {
		return fmt.Errorf("timeout_ms must be between 1 and %d", maxTimeoutMS)
	}

	parsed, err := url.Parse(c.URL)
	if err != nil {
		return fmt.Errorf("sender.api.url must be a valid absolute HTTP(S) URL: %w", err)
	}
	if !parsed.IsAbs() || parsed.Host == "" {
		return errors.New("url must be an absolute HTTP(S) URL with host")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("url must use http or https")
	}
	if parsed.Fragment != "" {
		return errors.New("url must not contain a fragment")
	}
	return nil
}

func (c workersConfig) validateWorkers() error {
	if c.Max < 1 || int64(c.Max) > maxExactJSONInteger || !c.Contains(c.Initial) {
		return errors.New("initial and max must be positive safe JSON integers, with initial <= max")
	}
	return nil
}

// Contains reports whether value is within the configured worker bounds.
func (c workersConfig) Contains(value int) bool {
	return value >= 1 && value <= c.Max
}

func (c tpsConfig) validateRequestedTPS() error {
	validMax := c.Max > 0 && int64(c.Max) <= maxExactJSONInteger
	if !validMax {
		return errors.New("max must be a positive safe JSON integer")
	}
	if !c.Contains(c.Initial) {
		return fmt.Errorf("initial=%d must be within 0..%d", c.Initial, c.Max)
	}
	return nil
}

// Contains reports whether value is within the configured TPS bounds.
func (c tpsConfig) Contains(value int) bool {
	return value >= 0 && value <= c.Max
}
