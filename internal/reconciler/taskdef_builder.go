// Package reconciler manages dynamic ECS task definition lifecycle from
// TOML configuration stored in SSM Parameter Store.
package reconciler

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/niranjan94/ecs-arc/internal/tomlcfg"
)

// InfraConfig holds environment-level config not stored in TOML.
type InfraConfig struct {
	ExecutionRoleARN string
	TaskRoleARN      string
	LogGroup         string
	Region           string
}

// BuildRegisterInput creates an ecs.RegisterTaskDefinitionInput from a
// resolved runner config and infrastructure config.
func BuildRegisterInput(cfg *tomlcfg.ResolvedRunnerConfig, infra InfraConfig) *ecs.RegisterTaskDefinitionInput {
	input := &ecs.RegisterTaskDefinitionInput{
		Family:      aws.String(cfg.Family),
		Cpu:         aws.String(fmt.Sprintf("%d", cfg.CPU)),
		Memory:      aws.String(fmt.Sprintf("%d", cfg.Memory)),
		NetworkMode: mapNetworkMode(cfg.NetworkMode),
		RequiresCompatibilities: []ecsTypes.Compatibility{
			mapCompatibility(cfg.Compatibility),
		},
		ExecutionRoleArn: aws.String(infra.ExecutionRoleARN),
		TaskRoleArn:      aws.String(infra.TaskRoleARN),
		Tags: []ecsTypes.Tag{
			{Key: aws.String("ecs-arc:managed"), Value: aws.String("true")},
		},
	}

	// RuntimePlatform
	if cfg.OS != "" {
		rp := &ecsTypes.RuntimePlatform{
			OperatingSystemFamily: ecsTypes.OSFamily(cfg.OS),
		}
		if cfg.Architecture != "" {
			rp.CpuArchitecture = ecsTypes.CPUArchitecture(cfg.Architecture)
		}
		input.RuntimePlatform = rp
	}

	if cfg.EnableDind {
		input.Volumes = dindVolumes()
		input.ContainerDefinitions = dindContainers(cfg, infra)
	} else {
		input.ContainerDefinitions = standaloneContainers(cfg, infra)
	}

	return input
}

func mapNetworkMode(mode string) ecsTypes.NetworkMode {
	switch mode {
	case "awsvpc":
		return ecsTypes.NetworkModeAwsvpc
	case "bridge":
		return ecsTypes.NetworkModeBridge
	default:
		return ecsTypes.NetworkMode(mode)
	}
}

func mapCompatibility(compat string) ecsTypes.Compatibility {
	switch compat {
	case "FARGATE":
		return ecsTypes.CompatibilityFargate
	case "EC2":
		return ecsTypes.CompatibilityEc2
	case "EXTERNAL":
		return ecsTypes.CompatibilityExternal
	default:
		return ecsTypes.Compatibility(compat)
	}
}

func logConfig(infra InfraConfig, streamPrefix string) *ecsTypes.LogConfiguration {
	return &ecsTypes.LogConfiguration{
		LogDriver: "awslogs",
		Options: map[string]string{
			"awslogs-group":         infra.LogGroup,
			"awslogs-region":        infra.Region,
			"awslogs-stream-prefix": streamPrefix,
		},
	}
}

func dindVolumes() []ecsTypes.Volume {
	return []ecsTypes.Volume{
		{Name: aws.String("dind-sock")},
		{Name: aws.String("dind-externals")},
		{Name: aws.String("work")},
	}
}

func standaloneContainers(cfg *tomlcfg.ResolvedRunnerConfig, infra InfraConfig) []ecsTypes.ContainerDefinition {
	return []ecsTypes.ContainerDefinition{
		{
			Name:             aws.String("runner"),
			Image:            aws.String(cfg.RunnerImage),
			Essential:        aws.Bool(true),
			Command:          []string{"/home/runner/run.sh"},
			LogConfiguration: logConfig(infra, cfg.Family),
		},
	}
}

func dindContainers(cfg *tomlcfg.ResolvedRunnerConfig, infra InfraConfig) []ecsTypes.ContainerDefinition {
	return []ecsTypes.ContainerDefinition{
		// init-dind-externals
		{
			Name:      aws.String("init-dind-externals"),
			Image:     aws.String(cfg.RunnerImage),
			Essential: aws.Bool(false),
			User:      aws.String("0"),
			Command:   []string{"sh", "-c", "cp -a /home/runner/externals/. /home/runner/tmpDir/ && chown -R 1001:123 /home/runner/tmpDir /home/runner/_work"},
			MountPoints: []ecsTypes.MountPoint{
				{SourceVolume: aws.String("dind-externals"), ContainerPath: aws.String("/home/runner/tmpDir")},
				{SourceVolume: aws.String("work"), ContainerPath: aws.String("/home/runner/_work")},
			},
			LogConfiguration: logConfig(infra, cfg.Family+"-init"),
		},
		// dind sidecar
		{
			Name:       aws.String("dind"),
			Image:      aws.String(cfg.DindImage),
			Essential:  aws.Bool(true),
			Privileged: aws.Bool(true),
			Command:    []string{"dockerd", "--host=unix:///var/run/docker.sock", "--group=123"},
			HealthCheck: &ecsTypes.HealthCheck{
				Command:     []string{"CMD", "docker", "info"},
				StartPeriod: aws.Int32(120),
				Interval:    aws.Int32(5),
				Timeout:     aws.Int32(5),
				Retries:     aws.Int32(3),
			},
			MountPoints: []ecsTypes.MountPoint{
				{SourceVolume: aws.String("work"), ContainerPath: aws.String("/home/runner/_work")},
				{SourceVolume: aws.String("dind-sock"), ContainerPath: aws.String("/var/run")},
				{SourceVolume: aws.String("dind-externals"), ContainerPath: aws.String("/home/runner/externals")},
			},
			LogConfiguration: logConfig(infra, cfg.Family+"-dind"),
		},
		// runner
		{
			Name:      aws.String("runner"),
			Image:     aws.String(cfg.RunnerImage),
			Essential: aws.Bool(true),
			Command:   []string{"/home/runner/run.sh"},
			Environment: []ecsTypes.KeyValuePair{
				{Name: aws.String("DOCKER_HOST"), Value: aws.String("unix:///var/run/docker.sock")},
				{Name: aws.String("RUNNER_WAIT_FOR_DOCKER_IN_SECONDS"), Value: aws.String("120")},
			},
			DependsOn: []ecsTypes.ContainerDependency{
				{ContainerName: aws.String("init-dind-externals"), Condition: ecsTypes.ContainerConditionSuccess},
				{ContainerName: aws.String("dind"), Condition: ecsTypes.ContainerConditionHealthy},
			},
			MountPoints: []ecsTypes.MountPoint{
				{SourceVolume: aws.String("work"), ContainerPath: aws.String("/home/runner/_work")},
				{SourceVolume: aws.String("dind-sock"), ContainerPath: aws.String("/var/run")},
			},
			LogConfiguration: logConfig(infra, cfg.Family),
		},
	}
}
