package main

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

// Configuration location.
const (
	defaultConfigPath = "config.toml"
	// JSON numbers must stay exact in the browser's numeric controls and telemetry.
	maxSnapshotInteger = 1<<53 - 1
)

type config struct {
	HTTP          httpConfig      `mapstructure:"http"`
	Source        sourceConfig    `mapstructure:"source"`
	Reader        readerConfig    `mapstructure:"reader"`
	ReaderChannel channelConfig   `mapstructure:"readerChannel"`
	SenderChannel channelConfig   `mapstructure:"senderChannel"`
	Throttler     throttlerConfig `mapstructure:"throttler"`
	Sender        senderConfig    `mapstructure:"sender"`
	Logging       loggingConfig   `mapstructure:"logging"`
}

type httpConfig struct {
	Addr string `mapstructure:"addr"`
}

type sourceConfig struct {
	Path string `mapstructure:"path" json:"path"`
}

type readerConfig struct {
	ReadBatchSize int           `mapstructure:"read_batch_size"`
	Workers       workersConfig `mapstructure:"workers"`
}

type channelConfig struct {
	Capacity int `mapstructure:"capacity"`
}

type throttlerConfig struct {
	RequestedTPS tpsConfig `mapstructure:"requested_tps"`
	Installed    bool      `mapstructure:"installed"`
}

type loggingConfig struct {
	Level string `mapstructure:"level" json:"level"`
}

type senderConfig struct {
	Workers workersConfig     `mapstructure:"workers"`
	API     senderAPIConfig   `mapstructure:"api"`
	Retry   senderRetryConfig `mapstructure:"retry"`
}

type senderAPIConfig struct {
	URL       string `mapstructure:"url"`
	TimeoutMS int64  `mapstructure:"timeout_ms"`
}

type senderRetryConfig struct {
	DelaysMS      []int `mapstructure:"delays_ms" json:"delaysMs"`
	JitterPercent int   `mapstructure:"jitter_percent" json:"jitterPercent"`
}

type workersConfig struct {
	Initial int `mapstructure:"initial" json:"initial"`
	Max     int `mapstructure:"max" json:"max"`
}

type tpsConfig struct {
	Initial int `mapstructure:"initial" json:"initial"`
	Max     int `mapstructure:"max" json:"max"`
	Step    int `mapstructure:"step" json:"step"`
}

func loadConfig(configPath string) (config, error) {
	path := defaultConfigPath
	if configPath != "" {
		path = configPath
	}

	absConfigPath, err := filepath.Abs(path)
	if err != nil {
		return config{}, fmt.Errorf("make config path absolute: %w", err)
	}

	data, err := os.ReadFile(absConfigPath)
	if err != nil {
		return config{}, fmt.Errorf("read config %q: %w", absConfigPath, err)
	}
	// Decode the complete TOML tree: flattened settings discard empty tables,
	// which would otherwise let unknown sections bypass strict decoding.
	var values map[string]any
	if err := toml.Unmarshal(data, &values); err != nil {
		return config{}, fmt.Errorf("read config %q: %w", absConfigPath, err)
	}
	if http, ok := configValue(values, "http").(map[string]any); !ok || configValue(http, "addr") == nil {
		return config{}, errors.New("http.addr is required")
	}
	var cfg config
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result: &cfg, TagName: "mapstructure", ErrorUnused: true, ErrorUnset: true,
		WeaklyTypedInput: false, DecodeHook: rejectFractionalInt,
	})
	if err != nil {
		return config{}, fmt.Errorf("create config decoder: %w", err)
	}
	if err := decoder.Decode(values); err != nil {
		return config{}, fmt.Errorf("decode config %q: %w", absConfigPath, err)
	}
	if err := cfg.validate(); err != nil {
		return config{}, fmt.Errorf("validate config %q: %w", absConfigPath, err)
	}

	return cfg, nil
}

func configValue(values map[string]any, key string) any {
	for name, value := range values {
		if strings.EqualFold(name, key) {
			return value
		}
	}
	return nil
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

func (c config) validate() error {
	if err := c.HTTP.validate(); err != nil {
		return fmt.Errorf("http.addr: %w", err)
	}
	if c.Source.Path == "" || !filepath.IsAbs(c.Source.Path) {
		return errors.New("source.path must be an absolute path")
	}
	if c.Reader.ReadBatchSize <= 0 || int64(c.Reader.ReadBatchSize) > math.MaxInt64/int64(time.Second) {
		return errors.New("reader.read_batch_size must be positive and within the pacing duration limit")
	}
	if err := c.Reader.Workers.validateWorkers(); err != nil {
		return fmt.Errorf("reader.workers: %w", err)
	}
	maxCapacity := min(
		int64(math.MaxInt)/int64(unsafe.Sizeof([]struct{}{})),
		int64(math.MaxInt)/int64(c.Reader.ReadBatchSize),
		maxSnapshotInteger/int64(c.Reader.ReadBatchSize),
	)
	for _, queue := range []struct {
		name     string
		capacity int
	}{
		{name: "readerChannel.capacity", capacity: c.ReaderChannel.Capacity},
		{name: "senderChannel.capacity", capacity: c.SenderChannel.Capacity},
	} {
		if queue.capacity < 0 || int64(queue.capacity) > maxCapacity {
			return fmt.Errorf("%s must be non-negative and its storage and buffered transactions must be representable", queue.name)
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
	if c.Max < 1 || int64(c.Max) > maxSnapshotInteger || !c.contains(c.Initial) {
		return errors.New("initial and max must be positive safe JSON integers, with initial <= max")
	}
	return nil
}

func (c workersConfig) contains(value int) bool {
	return value >= 1 && value <= c.Max
}

func (c tpsConfig) validateRequestedTPS() error {
	validMax := c.Max > 0 && int64(c.Max) <= maxSnapshotInteger
	validStep := c.Step > 0 && int64(c.Step) <= maxSnapshotInteger
	if !validMax || !validStep {
		return errors.New("max and step must be positive safe JSON integers")
	}
	if !c.contains(c.Initial) {
		return fmt.Errorf("initial=%d must be within 0..%d and aligned to step=%d", c.Initial, c.Max, c.Step)
	}
	return nil
}

func (c tpsConfig) contains(value int) bool {
	return value >= 0 && value <= c.Max && c.Step > 0 && value%c.Step == 0
}
