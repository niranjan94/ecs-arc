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
	ExtraLabels      []string `toml:"extra_labels"`
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

// Hardcoded defaults for fields not set in [defaults] or per-runner.
const (
	defaultRunnerImage   = "ghcr.io/niranjan94/ecs-arc-runner:latest"
	defaultDindImage     = "docker:dind"
	defaultCompatibility = "FARGATE"
	defaultNetworkMode   = "awsvpc"
	defaultMaxRunners    = 10
	defaultMinRunners    = 0
	defaultMaxRuntime    = "6h"
	defaultOS            = "LINUX"
)

// Resolve merges defaults into each runner, validates, and returns a map
// keyed by family name. Resolution order: per-runner > [defaults] > hardcoded.
func Resolve(cfg *Config) (map[string]*ResolvedRunnerConfig, error) {
	expanded, err := ExpandTemplates(cfg)
	if err != nil {
		return nil, err
	}

	allRunners := make([]RunnerConfig, 0, len(cfg.Runners)+len(expanded))
	allRunners = append(allRunners, cfg.Runners...)
	allRunners = append(allRunners, expanded...)

	results := make(map[string]*ResolvedRunnerConfig, len(allRunners))
	for _, r := range allRunners {
		if r.Family == "" {
			return nil, fmt.Errorf("runner missing required field: family")
		}
		if r.CPU == 0 {
			return nil, fmt.Errorf("runner %q missing required field: cpu", r.Family)
		}
		if r.Memory == 0 {
			return nil, fmt.Errorf("runner %q missing required field: memory", r.Family)
		}
		if _, exists := results[r.Family]; exists {
			return nil, fmt.Errorf("duplicate runner family: %q", r.Family)
		}

		resolved := &ResolvedRunnerConfig{
			Family:          r.Family,
			CPU:             r.CPU,
			Memory:          r.Memory,
			DimensionLabels: r.DimensionLabels,
		}

		// String fields: runner > defaults > hardcoded
		resolved.RunnerImage = resolveString(r.RunnerImage, cfg.Defaults.RunnerImage, defaultRunnerImage)
		resolved.DindImage = resolveString(r.DindImage, cfg.Defaults.DindImage, defaultDindImage)
		resolved.Compatibility = resolveString(r.Compatibility, cfg.Defaults.Compatibility, defaultCompatibility)
		resolved.NetworkMode = resolveString(r.NetworkMode, cfg.Defaults.NetworkMode, defaultNetworkMode)
		resolved.CapacityProvider = resolveString(r.CapacityProvider, cfg.Defaults.CapacityProvider, "")

		// OS: runner > hardcoded (no defaults-level OS in spec)
		resolved.OS = r.OS
		if resolved.OS == "" {
			resolved.OS = defaultOS
		}

		// Architecture: runner value directly (no default)
		resolved.Architecture = r.Architecture

		// Bool fields
		resolved.EnableDind = resolveBool(r.EnableDind, cfg.Defaults.EnableDind, false)

		// Int fields
		resolved.MaxRunners = resolveInt(r.MaxRunners, cfg.Defaults.MaxRunners, defaultMaxRunners)
		resolved.MinRunners = resolveInt(r.MinRunners, cfg.Defaults.MinRunners, defaultMinRunners)

		// Duration from string
		maxRuntimeStr := resolveString(r.MaxRuntime, cfg.Defaults.MaxRuntime, defaultMaxRuntime)
		resolved.MaxRuntime, err = time.ParseDuration(maxRuntimeStr)
		if err != nil {
			return nil, fmt.Errorf("runner %q: invalid max_runtime %q: %w", r.Family, maxRuntimeStr, err)
		}

		// Slice fields: runner > defaults (no hardcoded)
		resolved.Subnets = resolveSlice(r.Subnets, cfg.Defaults.Subnets)
		resolved.SecurityGroups = resolveSlice(r.SecurityGroups, cfg.Defaults.SecurityGroups)
		resolved.ExtraLabels = append(append([]string(nil), cfg.Defaults.ExtraLabels...), r.ExtraLabels...)

		// Validation
		if err := validateArchitecture(resolved.Architecture); err != nil {
			return nil, fmt.Errorf("runner %q: %w", r.Family, err)
		}

		// FARGATE + DinD conflict: force DinD off
		if resolved.Compatibility == "FARGATE" && resolved.EnableDind {
			resolved.EnableDind = false
		}

		// EXTERNAL forces bridge network mode
		if resolved.Compatibility == "EXTERNAL" {
			resolved.NetworkMode = "bridge"
			resolved.Subnets = nil
			resolved.SecurityGroups = nil
		}

		resolved.AutoLabels = DeriveAutoLabels(resolved)

		results[r.Family] = resolved
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no runners defined (need at least one [[runner]] or [[template]])")
	}

	return results, nil
}

func resolveString(runnerVal *string, defaultVal string, hardcoded string) string {
	if runnerVal != nil {
		return *runnerVal
	}
	if defaultVal != "" {
		return defaultVal
	}
	return hardcoded
}

func resolveBool(runnerVal *bool, defaultVal *bool, hardcoded bool) bool {
	if runnerVal != nil {
		return *runnerVal
	}
	if defaultVal != nil {
		return *defaultVal
	}
	return hardcoded
}

func resolveInt(runnerVal *int, defaultVal *int, hardcoded int) int {
	if runnerVal != nil {
		return *runnerVal
	}
	if defaultVal != nil {
		return *defaultVal
	}
	return hardcoded
}

func resolveSlice(runnerVal []string, defaultVal []string) []string {
	if len(runnerVal) > 0 {
		return runnerVal
	}
	return defaultVal
}

func validateArchitecture(arch string) error {
	switch arch {
	case "", "X86_64", "ARM64":
		return nil
	default:
		return fmt.Errorf("invalid architecture %q, must be one of: \"\", \"X86_64\", \"ARM64\"", arch)
	}
}
