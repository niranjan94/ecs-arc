// Package taskdef holds shared types for ECS task definition metadata
// used by the controller and scaler packages.
package taskdef

import (
	"fmt"
	"time"

	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// ScaleSetConfig holds the per-scale-set configuration.
type ScaleSetConfig struct {
	MaxRunners       int
	MinRunners       int
	MaxRuntime       time.Duration
	Subnets          []string
	SecurityGroups   []string
	CapacityProvider string
	// LaunchType is derived from the runner's compatibility setting.
	LaunchType ecsTypes.LaunchType
	// ExtraLabels are additional GitHub Actions labels for the scale set.
	ExtraLabels []string
}

// TaskDefInfo holds a resolved task definition and its parsed scale set config.
type TaskDefInfo struct {
	TaskDefinition *ecsTypes.TaskDefinition
	Config         ScaleSetConfig
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
