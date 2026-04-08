package taskdef

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

func TestParseScaleSetConfig_Defaults(t *testing.T) {
	tags := []ecsTypes.Tag{}
	defaults := Defaults{
		Subnets:          []string{"subnet-aaa"},
		SecurityGroups:   []string{"sg-xxx"},
		CapacityProvider: "cp-1",
	}

	cfg := ParseScaleSetConfig(tags, defaults)

	if cfg.MaxRunners != 10 {
		t.Errorf("MaxRunners = %d, want 10", cfg.MaxRunners)
	}
	if cfg.MinRunners != 0 {
		t.Errorf("MinRunners = %d, want 0", cfg.MinRunners)
	}
	if cfg.MaxRuntime != 6*time.Hour {
		t.Errorf("MaxRuntime = %v, want 6h", cfg.MaxRuntime)
	}
	if len(cfg.Subnets) != 1 || cfg.Subnets[0] != "subnet-aaa" {
		t.Errorf("Subnets = %v, want [subnet-aaa]", cfg.Subnets)
	}
}

func TestParseScaleSetConfig_CustomTags(t *testing.T) {
	tags := []ecsTypes.Tag{
		{Key: aws.String("ecs-arc:max-runners"), Value: aws.String("20")},
		{Key: aws.String("ecs-arc:min-runners"), Value: aws.String("3")},
		{Key: aws.String("ecs-arc:subnets"), Value: aws.String("subnet-bbb,subnet-ccc")},
		{Key: aws.String("ecs-arc:security-groups"), Value: aws.String("sg-yyy")},
		{Key: aws.String("ecs-arc:capacity-provider"), Value: aws.String("cp-custom")},
		{Key: aws.String("ecs-arc:max-runtime"), Value: aws.String("2h")},
	}

	cfg := ParseScaleSetConfig(tags, Defaults{})

	if cfg.MaxRunners != 20 {
		t.Errorf("MaxRunners = %d, want 20", cfg.MaxRunners)
	}
	if cfg.MinRunners != 3 {
		t.Errorf("MinRunners = %d, want 3", cfg.MinRunners)
	}
	if cfg.MaxRuntime != 2*time.Hour {
		t.Errorf("MaxRuntime = %v, want 2h", cfg.MaxRuntime)
	}
	if cfg.CapacityProvider != "cp-custom" {
		t.Errorf("CapacityProvider = %q, want %q", cfg.CapacityProvider, "cp-custom")
	}
}

func TestParseScaleSetConfig_InvalidTagsUseDefaults(t *testing.T) {
	tags := []ecsTypes.Tag{
		{Key: aws.String("ecs-arc:max-runners"), Value: aws.String("not-a-number")},
	}

	cfg := ParseScaleSetConfig(tags, Defaults{})

	if cfg.MaxRunners != 10 {
		t.Errorf("MaxRunners = %d, want 10 (default on parse error)", cfg.MaxRunners)
	}
}

func TestCheckFargatePrivileged(t *testing.T) {
	taskDef := &ecsTypes.TaskDefinition{
		RequiresCompatibilities: []ecsTypes.Compatibility{ecsTypes.CompatibilityFargate},
		ContainerDefinitions: []ecsTypes.ContainerDefinition{
			{Name: aws.String("runner"), Privileged: aws.Bool(true)},
		},
	}

	err := CheckFargatePrivileged(taskDef)
	if err == nil {
		t.Fatal("expected error for Fargate + privileged")
	}
}

type mockECSDescriber struct {
	taskDef *ecsTypes.TaskDefinition
	tags    []ecsTypes.Tag
	err     error
}

func (m *mockECSDescriber) DescribeTaskDefinition(_ context.Context, _ string) (*ecsTypes.TaskDefinition, []ecsTypes.Tag, error) {
	return m.taskDef, m.tags, m.err
}

func TestLoadAll_Success(t *testing.T) {
	mock := &mockECSDescriber{
		taskDef: &ecsTypes.TaskDefinition{
			TaskDefinitionArn: aws.String("arn:aws:ecs:us-east-1:123:task-definition/runner-small:1"),
		},
		tags: []ecsTypes.Tag{
			{Key: aws.String("ecs-arc:max-runners"), Value: aws.String("5")},
		},
	}
	defaults := Defaults{
		Subnets:        []string{"subnet-aaa"},
		SecurityGroups: []string{"sg-xxx"},
	}

	results, err := LoadAll(context.Background(), mock, []string{"runner-small"}, defaults)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results["runner-small"].Config.MaxRunners != 5 {
		t.Errorf("MaxRunners = %d, want 5", results["runner-small"].Config.MaxRunners)
	}
}

func TestBuildExtraLabels_WithRuntimePlatform(t *testing.T) {
	taskDef := &ecsTypes.TaskDefinition{
		RuntimePlatform: &ecsTypes.RuntimePlatform{
			OperatingSystemFamily: ecsTypes.OSFamilyLinux,
			CpuArchitecture:       ecsTypes.CPUArchitectureArm64,
		},
	}

	labels := buildExtraLabels(taskDef, []string{"self-hosted", "custom"})

	want := []string{"linux", "arm64", "self-hosted", "custom"}
	if len(labels) != len(want) {
		t.Fatalf("got %v, want %v", labels, want)
	}
	for i, l := range labels {
		if l != want[i] {
			t.Errorf("labels[%d] = %q, want %q", i, l, want[i])
		}
	}
}

func TestBuildExtraLabels_WithoutRuntimePlatform(t *testing.T) {
	taskDef := &ecsTypes.TaskDefinition{}

	labels := buildExtraLabels(taskDef, []string{"self-hosted"})

	if len(labels) != 1 || labels[0] != "self-hosted" {
		t.Errorf("got %v, want [self-hosted]", labels)
	}
}

func TestBuildExtraLabels_NoLabelsOrPlatform(t *testing.T) {
	taskDef := &ecsTypes.TaskDefinition{}

	labels := buildExtraLabels(taskDef, nil)

	if len(labels) != 0 {
		t.Errorf("got %v, want empty", labels)
	}
}

func TestResolveLaunchType(t *testing.T) {
	tests := []struct {
		name   string
		compat []ecsTypes.Compatibility
		want   ecsTypes.LaunchType
	}{
		{"Fargate", []ecsTypes.Compatibility{ecsTypes.CompatibilityFargate}, ecsTypes.LaunchTypeFargate},
		{"External", []ecsTypes.Compatibility{ecsTypes.CompatibilityExternal}, ecsTypes.LaunchTypeExternal},
		{"EC2", []ecsTypes.Compatibility{ecsTypes.CompatibilityEc2}, ecsTypes.LaunchTypeEc2},
		{"empty", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			taskDef := &ecsTypes.TaskDefinition{RequiresCompatibilities: tt.compat}
			got := resolveLaunchType(taskDef)
			if got != tt.want {
				t.Errorf("resolveLaunchType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoadAll_SetsLaunchType(t *testing.T) {
	mock := &mockECSDescriber{
		taskDef: &ecsTypes.TaskDefinition{
			TaskDefinitionArn:       aws.String("arn:aws:ecs:us-east-1:123:task-definition/runner-small:1"),
			RequiresCompatibilities: []ecsTypes.Compatibility{ecsTypes.CompatibilityExternal},
		},
		tags: []ecsTypes.Tag{},
	}

	results, err := LoadAll(context.Background(), mock, []string{"runner-small"}, Defaults{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results["runner-small"].Config.LaunchType != ecsTypes.LaunchTypeExternal {
		t.Errorf("LaunchType = %q, want EXTERNAL", results["runner-small"].Config.LaunchType)
	}
}

func TestLoadAll_ExtraLabelsFromRuntimePlatform(t *testing.T) {
	mock := &mockECSDescriber{
		taskDef: &ecsTypes.TaskDefinition{
			TaskDefinitionArn: aws.String("arn:aws:ecs:us-east-1:123:task-definition/runner-small:1"),
			RuntimePlatform: &ecsTypes.RuntimePlatform{
				OperatingSystemFamily: ecsTypes.OSFamilyLinux,
				CpuArchitecture:       ecsTypes.CPUArchitectureX8664,
			},
		},
		tags: []ecsTypes.Tag{},
	}
	defaults := Defaults{
		ExtraLabels: []string{"custom-label"},
	}

	results, err := LoadAll(context.Background(), mock, []string{"runner-small"}, defaults)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	labels := results["runner-small"].Config.ExtraLabels
	want := []string{"linux", "x86_64", "custom-label"}
	if len(labels) != len(want) {
		t.Fatalf("ExtraLabels = %v, want %v", labels, want)
	}
	for i, l := range labels {
		if l != want[i] {
			t.Errorf("ExtraLabels[%d] = %q, want %q", i, l, want[i])
		}
	}
}
