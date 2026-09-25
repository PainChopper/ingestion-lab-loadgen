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
	policySchemaVersion = 2
)

// Units accepted by policy fields.
const (
	sourceUnit        = "glob-pattern"
	batchSizeUnit     = "transactions"
	unitBatches       = "batches"
	requestedTPSUnit  = "transactions/s"
	metricsWindowUnit = "milliseconds"
	workersUnit       = "workers"
	percentUnit       = "percent"
)

// Mutability values accepted by policy fields.
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

type policy struct {
	SchemaVersion int                 `mapstructure:"schema_version"`
	Source        sourcePolicy        `mapstructure:"source"`
	Reader        readerPolicy        `mapstructure:"reader"`
	ReaderChannel readerChannelPolicy `mapstructure:"readerChannel"`
	SenderChannel readerChannelPolicy `mapstructure:"senderChannel"`
	Throttler     throttlerPolicy     `mapstructure:"throttler"`
	Sender        senderPolicy        `mapstructure:"sender"`
	Metrics       metricsPolicy       `mapstructure:"metrics"`
	Logging       loggingPolicy       `mapstructure:"logging"`
}

type sourcePolicy struct {
	Path       string `mapstructure:"path" json:"path"`
	Unit       string `mapstructure:"unit" json:"unit"`
	Mutability string `mapstructure:"mutability" json:"mutability"`
}

type readerPolicy struct {
	ReadBatchSize rangePolicy `mapstructure:"read_batch_size"`
	Workers       rangePolicy `mapstructure:"workers"`
}

type readerChannelPolicy struct {
	Capacity allowedPolicy `mapstructure:"capacity"`
}

type throttlerPolicy struct {
	RequestedTPS     rangePolicy            `mapstructure:"requested_tps"`
	InstallationMode installationModePolicy `mapstructure:"installation_mode"`
}

type metricsPolicy struct {
	WindowMS rangePolicy `mapstructure:"window_ms"`
}

type loggingPolicy struct {
	Level      string `mapstructure:"level" json:"level"`
	Mutability string `mapstructure:"mutability" json:"mutability"`
}

type senderPolicy struct {
	Workers rangePolicy       `mapstructure:"workers"`
	API     senderAPIPolicy   `mapstructure:"api"`
	Retry   senderRetryPolicy `mapstructure:"retry"`
}

type senderAPIPolicy struct {
	URL        string `mapstructure:"url"`
	Mutability string `mapstructure:"mutability"`
}

type senderRetryPolicy struct {
	MaxAttempts       int    `mapstructure:"max_attempts" json:"maxAttempts"`
	BackoffBaseMS     int    `mapstructure:"backoff_base_ms" json:"backoffBaseMs"`
	BackoffMultiplier int    `mapstructure:"backoff_multiplier" json:"backoffMultiplier"`
	JitterPercent     int    `mapstructure:"jitter_percent" json:"jitterPercent"`
	Mutability        string `mapstructure:"mutability" json:"mutability"`
}

type installationModePolicy struct {
	Default    string   `mapstructure:"default" json:"default"`
	Allowed    []string `mapstructure:"allowed" json:"allowed"`
	Mutability string   `mapstructure:"mutability" json:"mutability"`
}

type rangePolicy struct {
	Default    int    `mapstructure:"default" json:"default"`
	Min        int    `mapstructure:"min" json:"min"`
	Max        int    `mapstructure:"max" json:"max"`
	Step       int    `mapstructure:"step" json:"step"`
	Unit       string `mapstructure:"unit" json:"unit"`
	Mutability string `mapstructure:"mutability" json:"mutability"`
}

type allowedPolicy struct {
	Default    int    `mapstructure:"default" json:"default"`
	Allowed    []int  `mapstructure:"allowed" json:"allowed"`
	Unit       string `mapstructure:"unit" json:"unit"`
	Mutability string `mapstructure:"mutability" json:"mutability"`
}

func loadPolicy(configArgument string) (policy, string, error) {
	configPath := defaultConfigPath
	if configArgument != "" {
		configPath = configArgument
	}

	absConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		return policy{}, "", fmt.Errorf("make config path absolute: %w", err)
	}

	config := viper.New()
	config.SetConfigFile(absConfigPath)
	if err := config.ReadInConfig(); err != nil {
		return policy{}, "", fmt.Errorf("read config %q: %w", absConfigPath, err)
	}
	var loaded policy
	if err := config.UnmarshalExact(&loaded, func(decoderConfig *mapstructure.DecoderConfig) {
		decoderConfig.ErrorUnset = true
	}); err != nil {
		return policy{}, "", fmt.Errorf("decode config %q: %w", absConfigPath, err)
	}
	if err := loaded.validate(); err != nil {
		return policy{}, "", fmt.Errorf("validate config %q: %w", absConfigPath, err)
	}

	return loaded, absConfigPath, nil
}

func (p policy) validate() error {
	if p.SchemaVersion != policySchemaVersion {
		return fmt.Errorf("schema_version must be %d", policySchemaVersion)
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

func (p loggingPolicy) validate() error {
	if p.Mutability != startupOnly {
		return fmt.Errorf("mutability must be %q", startupOnly)
	}
	if !slices.Contains([]string{"debug", "info", "warn", "error"}, p.Level) {
		return fmt.Errorf("level must be debug, info, warn, or error")
	}
	return nil
}

func (p senderPolicy) validate() error {
	if err := p.Workers.validateExact(32, 1, 32, 1, workersUnit, immediate); err != nil {
		return fmt.Errorf("workers: %w", err)
	}
	if err := p.API.validate(); err != nil {
		return fmt.Errorf("api: %w", err)
	}
	if p.Retry != (senderRetryPolicy{MaxAttempts: 3, BackoffBaseMS: 250, BackoffMultiplier: 2, JitterPercent: 20, Mutability: startupOnly}) {
		return fmt.Errorf("retry must match the approved startup-only policy")
	}
	return nil
}

func (p senderAPIPolicy) validate() error {
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

func (p rangePolicy) validateExact(defaultValue, minValue, maxValue, stepValue int, unit, mutability string) error {
	if p.Default != defaultValue || p.Min != minValue || p.Max != maxValue || p.Step != stepValue || p.Unit != unit || p.Mutability != mutability {
		return fmt.Errorf("must match approved %s policy", unit)
	}
	return nil
}

func (p rangePolicy) validateWorkers() error {
	return p.validateExact(1, 1, 7, 1, workersUnit, immediate)
}

func (p rangePolicy) validateMetricsWindow() error {
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

func (p rangePolicy) validateRequestedTPS() error {
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

func (p installationModePolicy) validate() error {
	if p.Mutability != immediate || len(p.Allowed) != 2 ||
		!p.contains(throttlerInstalled) || !p.contains(throttlerBypass) ||
		!p.contains(p.Default) {
		return fmt.Errorf("allowed must be [%q, %q], default must be allowed, and mutability must be %q", throttlerInstalled, throttlerBypass, immediate)
	}
	return nil
}

func (p installationModePolicy) contains(value string) bool {
	return slices.Contains(p.Allowed, value)
}

func (p rangePolicy) validate() error {
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

func (p rangePolicy) contains(value int) bool {
	return value >= p.Min && value <= p.Max && (value-p.Min)%p.Step == 0
}

func (p allowedPolicy) validate() error {
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

func (p allowedPolicy) contains(value int) bool {
	return slices.Contains(p.Allowed, value)
}

func (p allowedPolicy) validateSenderChannelCapacity() error {
	if err := p.validate(); err != nil {
		return err
	}
	return nil
}

type policySnapshot struct {
	ReaderReadBatchSize       rangePolicy            `json:"readerReadBatchSize"`
	ReaderWorkers             rangePolicy            `json:"readerWorkers"`
	ReaderChannelCapacity     allowedPolicy          `json:"readerChannelCapacity"`
	SenderChannelCapacity     allowedPolicy          `json:"senderChannelCapacity"`
	ThrottlerRequestedTPS     rangePolicy            `json:"throttlerRequestedTps"`
	ThrottlerInstallationMode installationModePolicy `json:"throttlerInstallationMode"`
	MetricsWindowMS           rangePolicy            `json:"metricsWindowMs"`
	SenderWorkers             rangePolicy            `json:"senderWorkers"`
	SenderRetry               senderRetryPolicy      `json:"senderRetry"`
	Logging                   loggingPolicy          `json:"logging"`
}

func (p policy) snapshot() policySnapshot {
	return policySnapshot{
		ReaderReadBatchSize:       p.Reader.ReadBatchSize,
		ReaderWorkers:             p.Reader.Workers,
		ReaderChannelCapacity:     p.ReaderChannel.Capacity,
		SenderChannelCapacity:     p.SenderChannel.Capacity,
		ThrottlerRequestedTPS:     p.Throttler.RequestedTPS,
		ThrottlerInstallationMode: p.Throttler.InstallationMode,
		MetricsWindowMS:           p.Metrics.WindowMS,
		SenderWorkers:             p.Sender.Workers,
		SenderRetry:               p.Sender.Retry,
		Logging:                   p.Logging,
	}
}
