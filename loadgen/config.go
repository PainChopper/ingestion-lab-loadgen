package main

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// Configuration location and supported schema version.
const (
	defaultConfigPath   = "config.toml"
	configSchemaVersion = 3
)

const (
	sourceUnit        = "glob-pattern"
	batchSizeUnit     = "transactions"
	unitBatches       = "batches"
	requestedTPSUnit  = "transactions/s"
	metricsWindowUnit = "milliseconds"
	workersUnit       = "workers"
)

// Mutability values accepted by config fields.
const (
	startupOnly = "startup-only"
	immediate   = "immediate"
)

type config struct {
	SchemaVersion int             `mapstructure:"schema_version"`
	HTTP          httpConfig      `mapstructure:"http"`
	Source        sourceConfig    `mapstructure:"source"`
	Reader        readerConfig    `mapstructure:"reader"`
	ReaderChannel channelConfig   `mapstructure:"readerChannel"`
	SenderChannel channelConfig   `mapstructure:"senderChannel"`
	Throttler     throttlerConfig `mapstructure:"throttler"`
	Sender        senderConfig    `mapstructure:"sender"`
	Metrics       metricsConfig   `mapstructure:"metrics"`
	Logging       loggingConfig   `mapstructure:"logging"`
}

type httpConfig struct {
	Addr string `mapstructure:"addr"`
}

type sourceConfig struct {
	Path       string `mapstructure:"path" json:"path"`
	Unit       string `mapstructure:"unit" json:"unit"`
	Mutability string `mapstructure:"mutability" json:"mutability"`
}

type readerConfig struct {
	ReadBatchSize rangeConfig `mapstructure:"read_batch_size"`
	Workers       rangeConfig `mapstructure:"workers"`
}

type channelConfig struct {
	Capacity allowedConfig `mapstructure:"capacity"`
}

type throttlerConfig struct {
	RequestedTPS rangeConfig     `mapstructure:"requested_tps"`
	Installed    installedConfig `mapstructure:"installed"`
}

type metricsConfig struct {
	WindowMS rangeConfig `mapstructure:"window_ms"`
}

type loggingConfig struct {
	Level      string `mapstructure:"level" json:"level"`
	Mutability string `mapstructure:"mutability" json:"mutability"`
}

type senderConfig struct {
	Workers rangeConfig       `mapstructure:"workers"`
	API     senderAPIConfig   `mapstructure:"api"`
	Retry   senderRetryConfig `mapstructure:"retry"`
}

type senderAPIConfig struct {
	URL        string `mapstructure:"url"`
	TimeoutMS  int64  `mapstructure:"timeout_ms"`
	Mutability string `mapstructure:"mutability"`
}

type senderRetryConfig struct {
	DelaysMS      []int  `mapstructure:"delays_ms" json:"delaysMs"`
	JitterPercent int    `mapstructure:"jitter_percent" json:"jitterPercent"`
	Mutability    string `mapstructure:"mutability" json:"mutability"`
}

type installedConfig struct {
	Initial    bool   `mapstructure:"initial" json:"initial"`
	Allowed    []bool `mapstructure:"allowed" json:"allowed"`
	Mutability string `mapstructure:"mutability" json:"mutability"`
}

type rangeConfig struct {
	Initial    int    `mapstructure:"initial" json:"initial"`
	Min        int    `mapstructure:"min" json:"min"`
	Max        int    `mapstructure:"max" json:"max"`
	Step       int    `mapstructure:"step" json:"step"`
	Unit       string `mapstructure:"unit" json:"unit"`
	Mutability string `mapstructure:"mutability" json:"mutability"`
}

type allowedConfig struct {
	Initial    int    `mapstructure:"initial" json:"initial"`
	Allowed    []int  `mapstructure:"allowed" json:"allowed"`
	Unit       string `mapstructure:"unit" json:"unit"`
	Mutability string `mapstructure:"mutability" json:"mutability"`
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

	viperConfig := viper.New()
	viperConfig.SetConfigFile(absConfigPath)
	if err := viperConfig.ReadInConfig(); err != nil {
		return config{}, fmt.Errorf("read config %q: %w", absConfigPath, err)
	}
	if !viperConfig.IsSet("http.addr") {
		return config{}, errors.New("http.addr is required")
	}
	var cfg config
	if err := viperConfig.UnmarshalExact(&cfg, func(decoderConfig *mapstructure.DecoderConfig) {
		decoderConfig.ErrorUnset = true
		decoderConfig.WeaklyTypedInput = false
		decoderConfig.DecodeHook = rejectFractionalInt
	}); err != nil {
		return config{}, fmt.Errorf("decode config %q: %w", absConfigPath, err)
	}
	if err := cfg.validate(); err != nil {
		return config{}, fmt.Errorf("validate config %q: %w", absConfigPath, err)
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

func (c config) validate() error {
	if c.SchemaVersion != configSchemaVersion {
		return fmt.Errorf("schema_version must be %d", configSchemaVersion)
	}
	if err := c.HTTP.validate(); err != nil {
		return fmt.Errorf("http.addr: %w", err)
	}
	if c.Source.Path == "" || !filepath.IsAbs(c.Source.Path) {
		return errors.New("source.path must be an absolute path")
	}
	if c.Source.Unit != sourceUnit || c.Source.Mutability != startupOnly {
		return fmt.Errorf("source must use unit %q and mutability %q", sourceUnit, startupOnly)
	}
	if err := c.Reader.ReadBatchSize.validateBatchSize(); err != nil {
		return fmt.Errorf("reader.read_batch_size: %w", err)
	}
	if err := c.Reader.Workers.validateWorkers(); err != nil {
		return fmt.Errorf("reader.workers: %w", err)
	}
	if err := c.ReaderChannel.Capacity.validate(); err != nil {
		return fmt.Errorf("readerChannel.capacity: %w", err)
	}
	if err := c.SenderChannel.Capacity.validate(); err != nil {
		return fmt.Errorf("senderChannel.capacity: %w", err)
	}
	if int64(c.Reader.ReadBatchSize.Max) > math.MaxInt64/int64(time.Second) {
		return errors.New("reader.read_batch_size.max exceeds pacing duration limit")
	}
	if err := c.Throttler.RequestedTPS.validateRequestedTPS(); err != nil {
		return fmt.Errorf("throttler.requested_tps: %w", err)
	}
	if err := c.Throttler.Installed.validate(); err != nil {
		return fmt.Errorf("throttler.installed: %w", err)
	}
	if err := c.Sender.validate(); err != nil {
		return fmt.Errorf("sender: %w", err)
	}
	if err := c.Metrics.WindowMS.validateMetricsWindow(); err != nil {
		return fmt.Errorf("metrics.window_ms: %w", err)
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
	if c.Mutability != startupOnly {
		return fmt.Errorf("mutability must be %q", startupOnly)
	}
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
	if c.Mutability != startupOnly {
		return fmt.Errorf("mutability must be %q", startupOnly)
	}
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
	if c.Mutability != startupOnly {
		return fmt.Errorf("mutability must be %q", startupOnly)
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

func (c rangeConfig) validateWorkers() error {
	if c.Unit != workersUnit || c.Mutability != immediate {
		return fmt.Errorf("must use unit %q and mutability %q", workersUnit, immediate)
	}
	if c.Min < 1 || c.Max < c.Min || c.Step <= 0 {
		return errors.New("min, max, and step must form a positive range")
	}
	if !c.contains(c.Initial) {
		return errors.New("initial must be within the range and aligned to step from min")
	}
	return nil
}

func (c rangeConfig) validateMetricsWindow() error {
	if c.Unit != metricsWindowUnit || c.Mutability != startupOnly {
		return fmt.Errorf("must use unit %q and mutability %q", metricsWindowUnit, startupOnly)
	}
	if c.Min < 100 || c.Max > 10_000 || c.Max < c.Min || c.Step <= 0 {
		return errors.New("must stay within 100..10000 milliseconds with a positive step")
	}
	if !c.contains(c.Initial) {
		return errors.New("initial must be within the range and aligned to step")
	}
	return nil
}

func (c rangeConfig) validateRequestedTPS() error {
	if c.Unit != requestedTPSUnit || c.Mutability != immediate {
		return fmt.Errorf("must use unit %q and mutability %q", requestedTPSUnit, immediate)
	}
	if c.Min < 0 || c.Max <= c.Min || c.Step <= 0 {
		return errors.New("min, max, and step must form a non-negative range")
	}
	if c.Initial < c.Min || c.Initial > c.Max {
		return fmt.Errorf(
			"initial=%d is outside range min=%d max=%d (step=%d)",
			c.Initial,
			c.Min,
			c.Max,
			c.Step,
		)
	}
	if (c.Initial-c.Min)%c.Step != 0 {
		return fmt.Errorf(
			"initial=%d is not aligned to step=%d from min=%d (max=%d)",
			c.Initial,
			c.Step,
			c.Min,
			c.Max,
		)
	}
	return nil
}

func (c installedConfig) validate() error {
	if c.Mutability != immediate || len(c.Allowed) != 2 ||
		!c.contains(true) || !c.contains(false) ||
		!c.contains(c.Initial) {
		return fmt.Errorf("allowed must be [true, false], initial must be allowed, and mutability must be %q", immediate)
	}
	return nil
}

func (c installedConfig) contains(value bool) bool {
	return slices.Contains(c.Allowed, value)
}

func (c rangeConfig) validateBatchSize() error {
	if c.Unit != batchSizeUnit || c.Mutability != startupOnly {
		return fmt.Errorf("must use unit %q and mutability %q", batchSizeUnit, startupOnly)
	}
	if c.Min <= 0 || c.Max < c.Min || c.Step <= 0 {
		return errors.New("min, max, and step must form a positive range")
	}
	if !c.contains(c.Initial) {
		return errors.New("initial must be within the range and aligned to step")
	}
	return nil
}

func (c rangeConfig) contains(value int) bool {
	return value >= c.Min && value <= c.Max && (value-c.Min)%c.Step == 0
}

func (c allowedConfig) validate() error {
	if c.Unit != unitBatches || c.Mutability != startupOnly {
		return fmt.Errorf("must use unit %q and mutability %q", unitBatches, startupOnly)
	}
	if len(c.Allowed) == 0 {
		return errors.New("allowed must not be empty")
	}
	for index, value := range c.Allowed {
		if value < 0 {
			return fmt.Errorf("allowed[%d] must not be negative", index)
		}
		if index > 0 && value <= c.Allowed[index-1] {
			return errors.New("allowed must be strictly increasing")
		}
	}
	if !c.contains(c.Initial) {
		return errors.New("initial must be one of allowed")
	}
	return nil
}

func (c allowedConfig) contains(value int) bool {
	return slices.Contains(c.Allowed, value)
}
