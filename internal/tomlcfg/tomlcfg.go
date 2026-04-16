// Package tomlcfg parses and resolves TOML runner configuration for ecs-arc.
// It handles [defaults], explicit [[runner]] entries, and [[template]] matrix
// expansion with auto-derived labels.
package tomlcfg

import (
	"fmt"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the top-level TOML structure.
type Config struct {
	Defaults  DefaultsConfig   `toml:"defaults"`
	Runners   []RunnerConfig   `toml:"runner"`
	Templates []TemplateConfig `toml:"template"`
}

// DefaultsConfig holds global defaults inherited by all runners.
// Pointer fields distinguish "not set" from zero values.
type DefaultsConfig struct {
	RunnerImage      string   `toml:"runner_image"`
	DindImage        string   `toml:"dind_image"`
	Compatibility    string   `toml:"compatibility"`
	Subnets          []string `toml:"subnets"`
	SecurityGroups   []string `toml:"security_groups"`
	NetworkMode      string   `toml:"network_mode"`
	EnableDind       *bool    `toml:"enable_dind"`
	MaxRunners       *int     `toml:"max_runners"`
	MinRunners       *int     `toml:"min_runners"`
	MaxRuntime       string   `toml:"max_runtime"`
	CapacityProvider string   `toml:"capacity_provider"`
}

// RunnerConfig is one explicit [[runner]] entry. Pointer fields allow
// distinguishing "not set" from zero so we can fall through to defaults.
type RunnerConfig struct {
	Family           string   `toml:"family"`
	CPU              int      `toml:"cpu"`
	Memory           int      `toml:"memory"`
	Architecture     string   `toml:"architecture"`
	OS               string   `toml:"os"`
	ExtraLabels      []string `toml:"extra_labels"`
	RunnerImage      *string  `toml:"runner_image"`
	DindImage        *string  `toml:"dind_image"`
	Compatibility    *string  `toml:"compatibility"`
	Subnets          []string `toml:"subnets"`
	SecurityGroups   []string `toml:"security_groups"`
	NetworkMode      *string  `toml:"network_mode"`
	EnableDind       *bool    `toml:"enable_dind"`
	MaxRunners       *int     `toml:"max_runners"`
	MinRunners       *int     `toml:"min_runners"`
	MaxRuntime       *string  `toml:"max_runtime"`
	CapacityProvider *string  `toml:"capacity_provider"`
	// DimensionLabels are populated by template expansion, not from TOML.
	DimensionLabels []string `toml:"-"`
}

// DimensionEntry holds overrides for a single dimension value in a template.
type DimensionEntry struct {
	CPU              int      `toml:"cpu"`
	Memory           int      `toml:"memory"`
	Architecture     string   `toml:"architecture"`
	OS               string   `toml:"os"`
	EnableDind       *bool    `toml:"enable_dind"`
	MaxRunners       *int     `toml:"max_runners"`
	MinRunners       *int     `toml:"min_runners"`
	Compatibility    *string  `toml:"compatibility"`
	NetworkMode      *string  `toml:"network_mode"`
	RunnerImage      *string  `toml:"runner_image"`
	DindImage        *string  `toml:"dind_image"`
	MaxRuntime       *string  `toml:"max_runtime"`
	CapacityProvider *string  `toml:"capacity_provider"`
	ExtraLabels      []string `toml:"extra_labels"`
}

// ExcludeEntry matches a specific combination to exclude from expansion.
type ExcludeEntry struct {
	Size         string `toml:"size"`
	Architecture string `toml:"architecture"`
	Feature      string `toml:"feature"`
}

// ExcludeConfig holds the list of combinations to exclude.
type ExcludeConfig struct {
	Combinations []ExcludeEntry `toml:"combinations"`
}

// TemplateConfig defines a matrix of runners to generate via cross product.
type TemplateConfig struct {
	FamilyPrefix  string                    `toml:"family_prefix"`
	Sizes         map[string]DimensionEntry `toml:"sizes"`
	Architectures map[string]DimensionEntry `toml:"architectures"`
	Features      map[string]DimensionEntry `toml:"features"`
	Exclude       ExcludeConfig             `toml:"exclude"`
}

// ResolvedRunnerConfig is a RunnerConfig with all defaults applied.
// No pointer fields -- everything is resolved to a concrete value.
type ResolvedRunnerConfig struct {
	Family           string
	CPU              int
	Memory           int
	Architecture     string
	OS               string
	ExtraLabels      []string
	RunnerImage      string
	DindImage        string
	Compatibility    string
	Subnets          []string
	SecurityGroups   []string
	NetworkMode      string
	EnableDind       bool
	MaxRunners       int
	MinRunners       int
	MaxRuntime       time.Duration
	CapacityProvider string
	// DimensionLabels come from template dimension keys (e.g., "small", "x64", "docker").
	DimensionLabels []string
	// AutoLabels are derived from config fields (cpu, memory, architecture, etc.).
	AutoLabels []string
}

// Parse decodes TOML bytes into a Config.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse TOML: %w", err)
	}
	return &cfg, nil
}
