package main

import (
	"fmt"
	"math"
	"path/filepath"
	"time"

	"github.com/spf13/viper"
)

const (
	defaultConfigPath         = "config.toml"
	policySchemaVersion       = 2
	sourceUnit                = "glob-pattern"
	batchSizeUnit             = "transactions"
	readerChannelCapacityUnit = "batches"
	startupOnly               = "startup-only"
	idleOnly                  = "idle-only"
	requestedTPSUnit          = "transactions/s"
	immediate                 = "immediate"
	throttlerInstalled        = "installed"
	throttlerBypass           = "bypass"
)

var requiredPolicyKeys = []string{
	"schema_version",
	"source.path",
	"source.unit",
	"source.mutability",
	"reader.read_batch_size.default",
	"reader.read_batch_size.min",
	"reader.read_batch_size.max",
	"reader.read_batch_size.step",
	"reader.read_batch_size.unit",
	"reader.read_batch_size.mutability",
	"readerChannel.capacity.default",
	"readerChannel.capacity.allowed",
	"readerChannel.capacity.unit",
	"readerChannel.capacity.mutability",
	"senderChannel.capacity.default",
	"senderChannel.capacity.allowed",
	"senderChannel.capacity.unit",
	"senderChannel.capacity.mutability",
	"throttler.requested_tps.default",
	"throttler.requested_tps.min",
	"throttler.requested_tps.max",
	"throttler.requested_tps.step",
	"throttler.requested_tps.unit",
	"throttler.requested_tps.mutability",
	"throttler.installation_mode.default",
	"throttler.installation_mode.allowed",
	"throttler.installation_mode.mutability",
}

var senderChannelCapacityAllowed = []int{0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192}

type policy struct {
	SchemaVersion int                 `mapstructure:"schema_version"`
	Source        sourcePolicy        `mapstructure:"source"`
	Reader        readerPolicy        `mapstructure:"reader"`
	ReaderChannel readerChannelPolicy `mapstructure:"readerChannel"`
	SenderChannel readerChannelPolicy `mapstructure:"senderChannel"`
	Throttler     throttlerPolicy     `mapstructure:"throttler"`
}

type sourcePolicy struct {
	Path       string `mapstructure:"path" json:"path"`
	Unit       string `mapstructure:"unit" json:"unit"`
	Mutability string `mapstructure:"mutability" json:"mutability"`
}

type readerPolicy struct {
	ReadBatchSize rangePolicy `mapstructure:"read_batch_size"`
}

type readerChannelPolicy struct {
	Capacity allowedPolicy `mapstructure:"capacity"`
}

type throttlerPolicy struct {
	RequestedTPS     rangePolicy            `mapstructure:"requested_tps"`
	InstallationMode installationModePolicy `mapstructure:"installation_mode"`
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
	for _, key := range requiredPolicyKeys {
		if !config.IsSet(key) {
			return policy{}, "", fmt.Errorf("config %q is missing required key %q", absConfigPath, key)
		}
	}

	var loaded policy
	if err := config.UnmarshalExact(&loaded); err != nil {
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
	return nil
}

func (p rangePolicy) validateRequestedTPS() error {
	if p.Unit != requestedTPSUnit || p.Mutability != immediate {
		return fmt.Errorf("must use unit %q and mutability %q", requestedTPSUnit, immediate)
	}
	if p.Min < 0 || p.Max <= p.Min || p.Step <= 0 {
		return fmt.Errorf("min, max, and step must form a non-negative range")
	}
	if !p.contains(p.Default) {
		return fmt.Errorf("default must be within the range and aligned to step")
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
	for _, allowed := range p.Allowed {
		if value == allowed {
			return true
		}
	}
	return false
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
	if p.Unit != readerChannelCapacityUnit || p.Mutability != idleOnly {
		return fmt.Errorf("must use unit %q and mutability %q", readerChannelCapacityUnit, idleOnly)
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
	for _, allowed := range p.Allowed {
		if value == allowed {
			return true
		}
	}
	return false
}

func (p allowedPolicy) validateSenderChannelCapacity() error {
	if err := p.validate(); err != nil {
		return err
	}
	if p.Default != 0 || len(p.Allowed) != len(senderChannelCapacityAllowed) {
		return fmt.Errorf("must use default 0 and the approved allowed scale")
	}
	for index, value := range senderChannelCapacityAllowed {
		if p.Allowed[index] != value {
			return fmt.Errorf("must use default 0 and the approved allowed scale")
		}
	}
	return nil
}

type policySnapshot struct {
	ReaderReadBatchSize       rangePolicy            `json:"readerReadBatchSize"`
	ReaderChannelCapacity     allowedPolicy          `json:"readerChannelCapacity"`
	SenderChannelCapacity     allowedPolicy          `json:"senderChannelCapacity"`
	ThrottlerRequestedTPS     rangePolicy            `json:"throttlerRequestedTps"`
	ThrottlerInstallationMode installationModePolicy `json:"throttlerInstallationMode"`
}

func (p policy) snapshot() policySnapshot {
	return policySnapshot{
		ReaderReadBatchSize:       p.Reader.ReadBatchSize,
		ReaderChannelCapacity:     p.ReaderChannel.Capacity,
		SenderChannelCapacity:     p.SenderChannel.Capacity,
		ThrottlerRequestedTPS:     p.Throttler.RequestedTPS,
		ThrottlerInstallationMode: p.Throttler.InstallationMode,
	}
}
