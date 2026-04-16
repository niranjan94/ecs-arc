package taskdef

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

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

func TestCheckFargatePrivileged_NotFargate(t *testing.T) {
	taskDef := &ecsTypes.TaskDefinition{
		RequiresCompatibilities: []ecsTypes.Compatibility{ecsTypes.CompatibilityEc2},
		ContainerDefinitions: []ecsTypes.ContainerDefinition{
			{Name: aws.String("runner"), Privileged: aws.Bool(true)},
		},
	}

	err := CheckFargatePrivileged(taskDef)
	if err != nil {
		t.Fatalf("unexpected error for EC2 + privileged: %v", err)
	}
}

func TestCheckFargatePrivileged_FargateNotPrivileged(t *testing.T) {
	taskDef := &ecsTypes.TaskDefinition{
		RequiresCompatibilities: []ecsTypes.Compatibility{ecsTypes.CompatibilityFargate},
		ContainerDefinitions: []ecsTypes.ContainerDefinition{
			{Name: aws.String("runner")},
		},
	}

	err := CheckFargatePrivileged(taskDef)
	if err != nil {
		t.Fatalf("unexpected error for Fargate + not privileged: %v", err)
	}
}
