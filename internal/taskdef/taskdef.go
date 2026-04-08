// Package taskdef reads ECS task definition metadata and tags, producing
// per-scale-set configuration. It defines an ECSDescriber interface for
// testability and provides tag parsing with sensible defaults.
package taskdef

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

const (
	tagMaxRunners       = "ecs-arc:max-runners"
	tagMinRunners       = "ecs-arc:min-runners"
	tagSubnets          = "ecs-arc:subnets"
	tagSecurityGroups   = "ecs-arc:security-groups"
	tagCapacityProvider = "ecs-arc:capacity-provider"
	tagMaxRuntime       = "ecs-arc:max-runtime"

	defaultMaxRunners = 10
	defaultMinRunners = 0
	defaultMaxRuntime = 6 * time.Hour
)

// ECSDescriber is the interface for describing ECS task definitions.
// It exists to decouple this package from the AWS SDK for testing.
type ECSDescriber interface {
	DescribeTaskDefinition(ctx context.Context, family string) (*ecsTypes.TaskDefinition, []ecsTypes.Tag, error)
}

// ScaleSetConfig holds the per-scale-set configuration derived from
// task definition tags.
type ScaleSetConfig struct {
	MaxRunners       int
	MinRunners       int
	MaxRuntime       time.Duration
	Subnets          []string
	SecurityGroups   []string
	CapacityProvider string
	// ExtraLabels are additional GitHub Actions labels for the scale set,
	// including automatic OS/arch labels derived from the task definition's
	// RuntimePlatform and any user-specified labels.
	ExtraLabels []string
}

// Defaults holds the global default values that apply when tags are absent.
type Defaults struct {
	Subnets          []string
	SecurityGroups   []string
	CapacityProvider string
	ExtraLabels      []string
}

// TaskDefInfo holds a resolved task definition and its parsed scale set config.
type TaskDefInfo struct {
	TaskDefinition *ecsTypes.TaskDefinition
	Config         ScaleSetConfig
}

// ParseScaleSetConfig extracts ScaleSetConfig from task definition tags,
// falling back to the provided defaults for unset values.
func ParseScaleSetConfig(tags []ecsTypes.Tag, defaults Defaults) ScaleSetConfig {
	cfg := ScaleSetConfig{
		MaxRunners:       defaultMaxRunners,
		MinRunners:       defaultMinRunners,
		MaxRuntime:       defaultMaxRuntime,
		Subnets:          defaults.Subnets,
		SecurityGroups:   defaults.SecurityGroups,
		CapacityProvider: defaults.CapacityProvider,
	}

	tagMap := make(map[string]string, len(tags))
	for _, tag := range tags {
		if tag.Key != nil && tag.Value != nil {
			tagMap[*tag.Key] = *tag.Value
		}
	}

	if v, ok := tagMap[tagMaxRunners]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxRunners = n
		}
	}
	if v, ok := tagMap[tagMinRunners]; ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.MinRunners = n
		}
	}
	if v, ok := tagMap[tagMaxRuntime]; ok {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.MaxRuntime = d
		}
	}
	if v, ok := tagMap[tagSubnets]; ok {
		cfg.Subnets = splitCSV(v)
	}
	if v, ok := tagMap[tagSecurityGroups]; ok {
		cfg.SecurityGroups = splitCSV(v)
	}
	if v, ok := tagMap[tagCapacityProvider]; ok {
		cfg.CapacityProvider = v
	}

	return cfg
}

// CheckFargatePrivileged returns an error if the task definition requires
// Fargate compatibility and has any privileged containers.
func CheckFargatePrivileged(taskDef *ecsTypes.TaskDefinition) error {
	isFargate := false
	for _, c := range taskDef.RequiresCompatibilities {
		if c == ecsTypes.CompatibilityFargate {
			isFargate = true
			break
		}
	}
	if !isFargate {
		return nil
	}
	for _, cd := range taskDef.ContainerDefinitions {
		if cd.Privileged != nil && *cd.Privileged {
			name := ""
			if cd.Name != nil {
				name = *cd.Name
			}
			return fmt.Errorf("task definition has Fargate compatibility but container %q is privileged; Fargate does not support privileged mode", name)
		}
	}
	return nil
}

// LoadAll describes each task definition family and returns the resolved
// info with parsed tags. It fails fast on any error.
func LoadAll(ctx context.Context, describer ECSDescriber, families []string, defaults Defaults) (map[string]*TaskDefInfo, error) {
	results := make(map[string]*TaskDefInfo, len(families))
	for _, family := range families {
		taskDef, tags, err := describer.DescribeTaskDefinition(ctx, family)
		if err != nil {
			return nil, fmt.Errorf("failed to describe task definition %q: %w", family, err)
		}
		if err := CheckFargatePrivileged(taskDef); err != nil {
			return nil, fmt.Errorf("task definition %q: %w", family, err)
		}
		cfg := ParseScaleSetConfig(tags, defaults)
		cfg.ExtraLabels = buildExtraLabels(taskDef, defaults.ExtraLabels)
		results[family] = &TaskDefInfo{
			TaskDefinition: taskDef,
			Config:         cfg,
		}
	}
	return results, nil
}

func buildExtraLabels(taskDef *ecsTypes.TaskDefinition, userLabels []string) []string {
	var labels []string
	if taskDef.RuntimePlatform != nil {
		if os := string(taskDef.RuntimePlatform.OperatingSystemFamily); os != "" {
			labels = append(labels, strings.ToLower(os))
		}
		if arch := string(taskDef.RuntimePlatform.CpuArchitecture); arch != "" {
			labels = append(labels, strings.ToLower(arch))
		}
	}
	labels = append(labels, userLabels...)
	return labels
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
