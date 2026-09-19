package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/viper"
)

const (
	defaultConfigPath   = "config.toml"
	policySchemaVersion = 1
	sourceUnit          = "glob-pattern"
	batchSizeUnit       = "transactions"
	queueCapacityUnit   = "batches"
	startupOnly         = "startup-only"
	idleOnly            = "idle-only"
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
	"queue1.capacity.default",
	"queue1.capacity.allowed",
	"queue1.capacity.unit",
	"queue1.capacity.mutability",
}

type policy struct {
	SchemaVersion int          `mapstructure:"schema_version"`
	Source        sourcePolicy `mapstructure:"source"`
	Reader        readerPolicy `mapstructure:"reader"`
	Queue1        queue1Policy `mapstructure:"queue1"`
}

type sourcePolicy struct {
	Path       string `mapstructure:"path" json:"path"`
	Unit       string `mapstructure:"unit" json:"unit"`
	Mutability string `mapstructure:"mutability" json:"mutability"`
}

type readerPolicy struct {
	ReadBatchSize rangePolicy `mapstructure:"read_batch_size"`
}

type queue1Policy struct {
	Capacity allowedPolicy `mapstructure:"capacity"`
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
	if err := p.Queue1.Capacity.validate(); err != nil {
		return fmt.Errorf("queue1.capacity: %w", err)
	}
	return nil
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
	if p.Unit != queueCapacityUnit || p.Mutability != idleOnly {
		return fmt.Errorf("must use unit %q and mutability %q", queueCapacityUnit, idleOnly)
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

type policySnapshot struct {
	ReaderReadBatchSize rangePolicy   `json:"readerReadBatchSize"`
	Queue1Capacity      allowedPolicy `json:"queue1Capacity"`
}

func (p policy) snapshot() policySnapshot {
	return policySnapshot{
		ReaderReadBatchSize: p.Reader.ReadBatchSize,
		Queue1Capacity:      p.Queue1.Capacity,
	}
}
