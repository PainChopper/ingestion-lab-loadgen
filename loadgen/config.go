package main

import (
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"slices"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// Configuration location and supported schema version.
const (
	defaultConfigPath   = "config.toml"
	configSchemaVersion = 2
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
	idleOnly    = "idle-only"
	immediate   = "immediate"
)

// Values accepted by throttler installation mode.
const (
	throttlerInstalled = "installed"
	throttlerBypass    = "bypass"
)

type config struct {
	SchemaVersion int                 `mapstructure:"schema_version"`
	Source        sourceConfig        `mapstructure:"source"`
	Reader        readerConfig        `mapstructure:"reader"`
	ReaderChannel readerChannelConfig `mapstructure:"readerChannel"`
	SenderChannel readerChannelConfig `mapstructure:"senderChannel"`
	Throttler     throttlerConfig     `mapstructure:"throttler"`
	Sender        senderConfig        `mapstructure:"sender"`
	Metrics       metricsConfig       `mapstructure:"metrics"`
	Logging       loggingConfig       `mapstructure:"logging"`
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

type readerChannelConfig struct {
	Capacity allowedConfig `mapstructure:"capacity"`
}

type throttlerConfig struct {
	RequestedTPS     rangeConfig            `mapstructure:"requested_tps"`
	InstallationMode installationModeConfig `mapstructure:"installation_mode"`
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
	Mutability string `mapstructure:"mutability"`
}

type senderRetryConfig struct {
	DelaysMS      []int  `mapstructure:"delays_ms" json:"delaysMs"`
	JitterPercent int    `mapstructure:"jitter_percent" json:"jitterPercent"`
	Mutability    string `mapstructure:"mutability" json:"mutability"`
}

type installationModeConfig struct {
	Default    string   `mapstructure:"default" json:"default"`
	Allowed    []string `mapstructure:"allowed" json:"allowed"`
	Mutability string   `mapstructure:"mutability" json:"mutability"`
}

type rangeConfig struct {
	Default    int    `mapstructure:"default" json:"default"`
	Min        int    `mapstructure:"min" json:"min"`
	Max        int    `mapstructure:"max" json:"max"`
	Step       int    `mapstructure:"step" json:"step"`
	Unit       string `mapstructure:"unit" json:"unit"`
	Mutability string `mapstructure:"mutability" json:"mutability"`
}

type allowedConfig struct {
	Default    int    `mapstructure:"default" json:"default"`
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
	var cfg config
	if err := viperConfig.UnmarshalExact(&cfg, func(decoderConfig *mapstructure.DecoderConfig) {
		decoderConfig.ErrorUnset = true
	}); err != nil {
		return config{}, fmt.Errorf("decode config %q: %w", absConfigPath, err)
	}
	if err := cfg.validate(); err != nil {
		return config{}, fmt.Errorf("validate config %q: %w", absConfigPath, err)
	}

	return cfg, nil
}

func (p config) validate() error {
	if p.SchemaVersion != configSchemaVersion {
		return fmt.Errorf("schema_version must be %d", configSchemaVersion)
	}
	if p.Source.Path == "" || !filepath.IsAbs(p.Source.Path) {
		return fmt.Errorf("source.path must be an absolute path")
	}
	if p.Source.Unit != sourceUnit || p.Source.Mutability != startupOnly {
		return fmt.Errorf("source must use unit %q and mutability %q", sourceUnit, startupOnly)
	}
	if err := p.Reader.ReadBatchSize.validate(); err != nil {
		return fmt.Errorf("reader.read_batch_size: %w", err)
	}
	if err := p.Reader.Workers.validateWorkers(); err != nil {
		return fmt.Errorf("reader.workers: %w", err)
	}
	if err := p.ReaderChannel.Capacity.validate(); err != nil {
		return fmt.Errorf("readerChannel.capacity: %w", err)
	}
	if err := p.SenderChannel.Capacity.validateSenderChannelCapacity(); err != nil {
		return fmt.Errorf("senderChannel.capacity: %w", err)
	}
	if int64(p.Reader.ReadBatchSize.Max) > math.MaxInt64/int64(time.Second) {
		return fmt.Errorf("reader.read_batch_size.max exceeds pacing duration limit")
	}
	if err := p.Throttler.RequestedTPS.validateRequestedTPS(); err != nil {
		return fmt.Errorf("throttler.requested_tps: %w", err)
	}
	if err := p.Throttler.InstallationMode.validate(); err != nil {
		return fmt.Errorf("throttler.installation_mode: %w", err)
	}
	if err := p.Sender.validate(); err != nil {
		return fmt.Errorf("sender: %w", err)
	}
	if err := p.Metrics.WindowMS.validateMetricsWindow(); err != nil {
		return fmt.Errorf("metrics.window_ms: %w", err)
	}
	if err := p.Logging.validate(); err != nil {
		return fmt.Errorf("logging: %w", err)
	}
	return nil
}

func (p loggingConfig) validate() error {
	if p.Mutability != startupOnly {
		return fmt.Errorf("mutability must be %q", startupOnly)
	}
	if !slices.Contains([]string{"debug", "info", "warn", "error"}, p.Level) {
		return fmt.Errorf("level must be debug, info, warn, or error")
	}
	return nil
}

func (p senderConfig) validate() error {
	if err := p.Workers.validateExact(32, 1, 32, 1, workersUnit, immediate); err != nil {
		return fmt.Errorf("workers: %w", err)
	}
	if err := p.API.validate(); err != nil {
		return fmt.Errorf("api: %w", err)
	}
	return p.Retry.validate()
}

func (p senderRetryConfig) validate() error {
	if p.Mutability != startupOnly {
		return fmt.Errorf("mutability must be %q", startupOnly)
	}
	if p.JitterPercent != 20 {
		return fmt.Errorf("jitter_percent must be 20")
	}
	if len(p.DelaysMS) == 0 {
		return fmt.Errorf("delays_ms must not be empty")
	}
	for index, delay := range p.DelaysMS {
		if delay <= 0 {
			return fmt.Errorf("delays_ms[%d] must be positive", index)
		}
		if int64(delay) > senderRetryMaxDelayMS(p.JitterPercent) {
			return fmt.Errorf("delays_ms[%d] exceeds the maximum safe retry duration", index)
		}
		if index > 0 && delay < p.DelaysMS[index-1] {
			return fmt.Errorf("delays_ms must be nondecreasing")
		}
	}
	return nil
}

func (p senderRetryConfig) delay(attempt int, batch []Transaction) time.Duration {
	index := min(attempt-1, len(p.DelaysMS)-1)
	jitter := deterministicSenderValue(batch, attempt, uint64(2*p.JitterPercent+1)) - p.JitterPercent
	return senderRetryDuration(p.DelaysMS[index], jitter)
}

func senderRetryMaxDelayMS(jitterPercent int) int64 {
	factor := int64(time.Millisecond)
	if jitterPercent > 0 {
		factor += factor * int64(jitterPercent) / 100
	}
	return math.MaxInt64 / factor
}

func senderRetryDuration(delayMS, jitterPercent int) time.Duration {
	if delayMS <= 0 || int64(delayMS) > math.MaxInt64/int64(time.Millisecond) {
		return time.Duration(math.MaxInt64)
	}
	base := time.Duration(delayMS) * time.Millisecond
	adjustment := base/100*time.Duration(jitterPercent) + base%100*time.Duration(jitterPercent)/100
	if adjustment > 0 && adjustment > time.Duration(math.MaxInt64)-base {
		return time.Duration(math.MaxInt64)
	}
	delay := base + adjustment
	if delay <= 0 {
		return time.Millisecond
	}
	return delay
}

func (p senderAPIConfig) validate() error {
	if p.URL == "" {
		return fmt.Errorf("url must not be empty")
	}
	if p.Mutability != startupOnly {
		return fmt.Errorf("mutability must be %q", startupOnly)
	}

	parsed, err := url.Parse(p.URL)
	if err != nil {
		return fmt.Errorf("url must be a valid absolute HTTP(S) URL")
	}
	if !parsed.IsAbs() || parsed.Host == "" {
		return fmt.Errorf("url must be an absolute HTTP(S) URL with host")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("url must use http or https")
	}
	if parsed.Fragment != "" {
		return fmt.Errorf("url must not contain a fragment")
	}
	return nil
}

func (p rangeConfig) validateExact(defaultValue, minValue, maxValue, stepValue int, unit, mutability string) error {
	if p.Default != defaultValue || p.Min != minValue || p.Max != maxValue || p.Step != stepValue || p.Unit != unit || p.Mutability != mutability {
		return fmt.Errorf("must match approved %s config", unit)
	}
	return nil
}

func (p rangeConfig) validateWorkers() error {
	return p.validateExact(1, 1, 7, 1, workersUnit, immediate)
}

func (p rangeConfig) validateMetricsWindow() error {
	if p.Unit != metricsWindowUnit || p.Mutability != startupOnly {
		return fmt.Errorf("must use unit %q and mutability %q", metricsWindowUnit, startupOnly)
	}
	if p.Min < 100 || p.Max > 10_000 || p.Max < p.Min || p.Step <= 0 {
		return fmt.Errorf("must stay within 100..10000 milliseconds with a positive step")
	}
	if !p.contains(p.Default) {
		return fmt.Errorf("default must be within the range and aligned to step")
	}
	return nil
}

func (p rangeConfig) validateRequestedTPS() error {
	if p.Unit != requestedTPSUnit || p.Mutability != immediate {
		return fmt.Errorf("must use unit %q and mutability %q", requestedTPSUnit, immediate)
	}
	if p.Min < 0 || p.Max <= p.Min || p.Step <= 0 {
		return fmt.Errorf("min, max, and step must form a non-negative range")
	}
	if p.Default < p.Min || p.Default > p.Max {
		return fmt.Errorf(
			"default=%d is outside range min=%d max=%d (step=%d)",
			p.Default,
			p.Min,
			p.Max,
			p.Step,
		)
	}
	if (p.Default-p.Min)%p.Step != 0 {
		return fmt.Errorf(
			"default=%d is not aligned to step=%d from min=%d (max=%d)",
			p.Default,
			p.Step,
			p.Min,
			p.Max,
		)
	}
	return nil
}

func (p installationModeConfig) validate() error {
	if p.Mutability != immediate || len(p.Allowed) != 2 ||
		!p.contains(throttlerInstalled) || !p.contains(throttlerBypass) ||
		!p.contains(p.Default) {
		return fmt.Errorf("allowed must be [%q, %q], default must be allowed, and mutability must be %q", throttlerInstalled, throttlerBypass, immediate)
	}
	return nil
}

func (p installationModeConfig) contains(value string) bool {
	return slices.Contains(p.Allowed, value)
}

func (p rangeConfig) validate() error {
	if p.Unit != batchSizeUnit || p.Mutability != idleOnly {
		return fmt.Errorf("must use unit %q and mutability %q", batchSizeUnit, idleOnly)
	}
	if p.Min <= 0 || p.Max < p.Min || p.Step <= 0 {
		return fmt.Errorf("min, max, and step must form a positive range")
	}
	if !p.contains(p.Default) {
		return fmt.Errorf("default must be within the range and aligned to step")
	}
	return nil
}

func (p rangeConfig) contains(value int) bool {
	return value >= p.Min && value <= p.Max && (value-p.Min)%p.Step == 0
}

func (p allowedConfig) validate() error {
	if p.Unit != unitBatches || p.Mutability != idleOnly {
		return fmt.Errorf("must use unit %q and mutability %q", unitBatches, idleOnly)
	}
	if len(p.Allowed) == 0 {
		return fmt.Errorf("allowed must not be empty")
	}
	for index, value := range p.Allowed {
		if value < 0 {
			return fmt.Errorf("allowed[%d] must not be negative", index)
		}
		if index > 0 && value <= p.Allowed[index-1] {
			return fmt.Errorf("allowed must be strictly increasing")
		}
	}
	if !p.contains(p.Default) {
		return fmt.Errorf("default must be one of allowed")
	}
	return nil
}

func (p allowedConfig) contains(value int) bool {
	return slices.Contains(p.Allowed, value)
}

func (p allowedConfig) validateSenderChannelCapacity() error {
	if err := p.validate(); err != nil {
		return err
	}
	return nil
}
