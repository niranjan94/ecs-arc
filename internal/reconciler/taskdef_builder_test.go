package reconciler

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/niranjan94/ecs-arc/internal/tomlcfg"
)

var testInfra = InfraConfig{
	ExecutionRoleARN: "arn:aws:iam::123:role/exec",
	TaskRoleARN:      "arn:aws:iam::123:role/task",
	LogGroup:         "/ecs/runners",
	Region:           "us-east-1",
}

func TestBuildRegisterInput_NoDinD(t *testing.T) {
	cfg := &tomlcfg.ResolvedRunnerConfig{
		Family:        "runner-small",
		CPU:           1024,
		Memory:        2048,
		Compatibility: "FARGATE",
		NetworkMode:   "awsvpc",
		OS:            "LINUX",
		EnableDind:    false,
		RunnerImage:   "ghcr.io/actions/actions-runner:latest",
		MaxRuntime:    6 * time.Hour,
	}

	input := BuildRegisterInput(cfg, testInfra)

	if aws.ToString(input.Family) != "runner-small" {
		t.Errorf("Family = %q", aws.ToString(input.Family))
	}
	if aws.ToString(input.Cpu) != "1024" {
		t.Errorf("Cpu = %q, want 1024", aws.ToString(input.Cpu))
	}
	if aws.ToString(input.Memory) != "2048" {
		t.Errorf("Memory = %q, want 2048", aws.ToString(input.Memory))
	}
	if input.NetworkMode != ecsTypes.NetworkModeAwsvpc {
		t.Errorf("NetworkMode = %v, want awsvpc", input.NetworkMode)
	}
	if len(input.RequiresCompatibilities) != 1 || input.RequiresCompatibilities[0] != ecsTypes.CompatibilityFargate {
		t.Errorf("RequiresCompatibilities = %v, want [FARGATE]", input.RequiresCompatibilities)
	}

	// No DinD: 1 container, no volumes
	if len(input.ContainerDefinitions) != 1 {
		t.Fatalf("ContainerDefinitions = %d, want 1", len(input.ContainerDefinitions))
	}
	if aws.ToString(input.ContainerDefinitions[0].Name) != "runner" {
		t.Errorf("container name = %q, want runner", aws.ToString(input.ContainerDefinitions[0].Name))
	}
	if len(input.Volumes) != 0 {
		t.Errorf("Volumes = %d, want 0", len(input.Volumes))
	}
}

func TestBuildRegisterInput_WithDinD(t *testing.T) {
	cfg := &tomlcfg.ResolvedRunnerConfig{
		Family:        "runner-large-docker",
		CPU:           4096,
		Memory:        8192,
		Compatibility: "EC2",
		NetworkMode:   "awsvpc",
		OS:            "LINUX",
		EnableDind:    true,
		RunnerImage:   "ghcr.io/actions/actions-runner:latest",
		DindImage:     "docker:dind",
		MaxRuntime:    6 * time.Hour,
	}

	input := BuildRegisterInput(cfg, testInfra)

	// DinD: 3 containers, 3 volumes
	if len(input.ContainerDefinitions) != 3 {
		t.Fatalf("ContainerDefinitions = %d, want 3", len(input.ContainerDefinitions))
	}
	if len(input.Volumes) != 3 {
		t.Errorf("Volumes = %d, want 3", len(input.Volumes))
	}

	// Verify container names
	names := make(map[string]bool)
	for _, cd := range input.ContainerDefinitions {
		names[aws.ToString(cd.Name)] = true
	}
	for _, want := range []string{"init-dind-externals", "dind", "runner"} {
		if !names[want] {
			t.Errorf("missing container %q", want)
		}
	}

	// Verify dind is privileged
	for _, cd := range input.ContainerDefinitions {
		if aws.ToString(cd.Name) == "dind" {
			if cd.Privileged == nil || !*cd.Privileged {
				t.Error("dind container must be privileged")
			}
			if cd.HealthCheck == nil {
				t.Error("dind container must have health check")
			}
		}
	}

	// Verify runner has DOCKER_HOST env var
	for _, cd := range input.ContainerDefinitions {
		if aws.ToString(cd.Name) == "runner" {
			found := false
			for _, env := range cd.Environment {
				if aws.ToString(env.Name) == "DOCKER_HOST" {
					found = true
					if aws.ToString(env.Value) != "unix:///var/run/docker.sock" {
						t.Errorf("DOCKER_HOST = %q", aws.ToString(env.Value))
					}
				}
			}
			if !found {
				t.Error("runner container missing DOCKER_HOST env var")
			}
			// Verify runner depends on init + dind
			if len(cd.DependsOn) != 2 {
				t.Errorf("runner DependsOn = %d, want 2", len(cd.DependsOn))
			}
		}
	}
}

func TestBuildRegisterInput_CompatibilityMapping(t *testing.T) {
	tests := []struct {
		compat string
		want   ecsTypes.Compatibility
	}{
		{"FARGATE", ecsTypes.CompatibilityFargate},
		{"EC2", ecsTypes.CompatibilityEc2},
		{"EXTERNAL", ecsTypes.CompatibilityExternal},
	}
	for _, tt := range tests {
		cfg := &tomlcfg.ResolvedRunnerConfig{
			Family: "test-" + tt.compat, CPU: 1024, Memory: 2048,
			Compatibility: tt.compat, NetworkMode: "awsvpc", OS: "LINUX",
			RunnerImage: "runner:latest", MaxRuntime: 6 * time.Hour,
		}
		input := BuildRegisterInput(cfg, testInfra)
		if len(input.RequiresCompatibilities) != 1 || input.RequiresCompatibilities[0] != tt.want {
			t.Errorf("compat %q: RequiresCompatibilities = %v, want [%v]", tt.compat, input.RequiresCompatibilities, tt.want)
		}
	}
}

func TestBuildRegisterInput_ArchitectureSet(t *testing.T) {
	cfg := &tomlcfg.ResolvedRunnerConfig{
		Family: "test", CPU: 1024, Memory: 2048, Architecture: "ARM64",
		Compatibility: "FARGATE", NetworkMode: "awsvpc", OS: "LINUX",
		RunnerImage: "runner:latest", MaxRuntime: 6 * time.Hour,
	}
	input := BuildRegisterInput(cfg, testInfra)
	if input.RuntimePlatform == nil {
		t.Fatal("RuntimePlatform is nil")
	}
	if input.RuntimePlatform.CpuArchitecture != ecsTypes.CPUArchitectureArm64 {
		t.Errorf("CpuArchitecture = %v, want ARM64", input.RuntimePlatform.CpuArchitecture)
	}
}

func TestBuildRegisterInput_ArchitectureEmpty(t *testing.T) {
	cfg := &tomlcfg.ResolvedRunnerConfig{
		Family: "test", CPU: 1024, Memory: 2048, Architecture: "",
		Compatibility: "FARGATE", NetworkMode: "awsvpc", OS: "LINUX",
		RunnerImage: "runner:latest", MaxRuntime: 6 * time.Hour,
	}
	input := BuildRegisterInput(cfg, testInfra)
	// RuntimePlatform should still exist (for OS) but CpuArchitecture should be empty
	if input.RuntimePlatform != nil && input.RuntimePlatform.CpuArchitecture != "" {
		t.Errorf("CpuArchitecture = %v, want empty", input.RuntimePlatform.CpuArchitecture)
	}
}

func TestBuildRegisterInput_ManagedTag(t *testing.T) {
	cfg := &tomlcfg.ResolvedRunnerConfig{
		Family: "test", CPU: 1024, Memory: 2048,
		Compatibility: "FARGATE", NetworkMode: "awsvpc", OS: "LINUX",
		RunnerImage: "runner:latest", MaxRuntime: 6 * time.Hour,
	}
	input := BuildRegisterInput(cfg, testInfra)
	found := false
	for _, tag := range input.Tags {
		if aws.ToString(tag.Key) == "ecs-arc:managed" && aws.ToString(tag.Value) == "true" {
			found = true
		}
	}
	if !found {
		t.Error("missing ecs-arc:managed tag")
	}
}

func TestBuildRegisterInput_Roles(t *testing.T) {
	cfg := &tomlcfg.ResolvedRunnerConfig{
		Family: "test", CPU: 1024, Memory: 2048,
		Compatibility: "FARGATE", NetworkMode: "awsvpc", OS: "LINUX",
		RunnerImage: "runner:latest", MaxRuntime: 6 * time.Hour,
	}
	input := BuildRegisterInput(cfg, testInfra)
	if aws.ToString(input.ExecutionRoleArn) != testInfra.ExecutionRoleARN {
		t.Errorf("ExecutionRoleArn = %q", aws.ToString(input.ExecutionRoleArn))
	}
	if aws.ToString(input.TaskRoleArn) != testInfra.TaskRoleARN {
		t.Errorf("TaskRoleArn = %q", aws.ToString(input.TaskRoleArn))
	}
}

func TestBuildRegisterInput_LogStreamPrefix(t *testing.T) {
	cfg := &tomlcfg.ResolvedRunnerConfig{
		Family: "runner-small", CPU: 1024, Memory: 2048,
		Compatibility: "EC2", NetworkMode: "awsvpc", OS: "LINUX",
		EnableDind: true, RunnerImage: "runner:latest", DindImage: "docker:dind",
		MaxRuntime: 6 * time.Hour,
	}
	input := BuildRegisterInput(cfg, testInfra)
	for _, cd := range input.ContainerDefinitions {
		name := aws.ToString(cd.Name)
		if cd.LogConfiguration == nil {
			t.Errorf("container %q missing log configuration", name)
			continue
		}
		prefix := cd.LogConfiguration.Options["awslogs-stream-prefix"]
		switch name {
		case "runner":
			if prefix != "runner-small" {
				t.Errorf("runner stream prefix = %q, want runner-small", prefix)
			}
		case "dind":
			if prefix != "runner-small-dind" {
				t.Errorf("dind stream prefix = %q, want runner-small-dind", prefix)
			}
		case "init-dind-externals":
			if prefix != "runner-small-init" {
				t.Errorf("init stream prefix = %q, want runner-small-init", prefix)
			}
		}
	}
}
