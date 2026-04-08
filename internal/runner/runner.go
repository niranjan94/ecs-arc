// Package runner provides ECS task management for GitHub Actions runners.
// It defines a TaskRunner interface for testability and an ECS implementation
// that creates, stops, and lists runner tasks.
package runner

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// ECSClient is the subset of the ECS API that the runner package needs.
type ECSClient interface {
	RunTask(ctx context.Context, input *ecs.RunTaskInput, opts ...func(*ecs.Options)) (*ecs.RunTaskOutput, error)
	StopTask(ctx context.Context, input *ecs.StopTaskInput, opts ...func(*ecs.Options)) (*ecs.StopTaskOutput, error)
	ListTasks(ctx context.Context, input *ecs.ListTasksInput, opts ...func(*ecs.Options)) (*ecs.ListTasksOutput, error)
	DescribeTasks(ctx context.Context, input *ecs.DescribeTasksInput, opts ...func(*ecs.Options)) (*ecs.DescribeTasksOutput, error)
}

// RunTaskInput holds the parameters for creating a new runner ECS task.
type RunTaskInput struct {
	TaskDefinition   string
	JITConfigEncoded string
	RunnerName       string
	ScaleSetName     string
	Subnets          []string
	SecurityGroups   []string
	CapacityProvider string
	LaunchType       ecsTypes.LaunchType
}

// ECSRunner manages ECS tasks for GitHub Actions runners.
type ECSRunner struct {
	client  ECSClient
	cluster string
	logger  *slog.Logger
}

// NewECSRunner creates a new ECSRunner.
func NewECSRunner(client ECSClient, cluster string, logger *slog.Logger) *ECSRunner {
	return &ECSRunner{
		client:  client,
		cluster: cluster,
		logger:  logger,
	}
}

// RunTask creates a new ECS task for a GitHub Actions runner. It sets the
// ACTIONS_RUNNER_INPUT_JITCONFIG environment variable so the runner binary
// can authenticate without a separate registration step.
func (r *ECSRunner) RunTask(ctx context.Context, input RunTaskInput) (string, error) {
	ecsInput := &ecs.RunTaskInput{
		Cluster:        aws.String(r.cluster),
		TaskDefinition: aws.String(input.TaskDefinition),
		Count:          aws.Int32(1),
		Overrides: &ecsTypes.TaskOverride{
			ContainerOverrides: []ecsTypes.ContainerOverride{
				{
					Name: aws.String("runner"),
					Environment: []ecsTypes.KeyValuePair{
						{
							Name:  aws.String("ACTIONS_RUNNER_INPUT_JITCONFIG"),
							Value: aws.String(input.JITConfigEncoded),
						},
					},
				},
			},
		},
		Tags: []ecsTypes.Tag{
			{Key: aws.String("ecs-arc:scale-set"), Value: aws.String(input.ScaleSetName)},
			{Key: aws.String("ecs-arc:runner-name"), Value: aws.String(input.RunnerName)},
		},
		EnableECSManagedTags: true,
		PropagateTags:        ecsTypes.PropagateTagsTaskDefinition,
	}

	// Network configuration for awsvpc mode
	if len(input.Subnets) > 0 {
		ecsInput.NetworkConfiguration = &ecsTypes.NetworkConfiguration{
			AwsvpcConfiguration: &ecsTypes.AwsVpcConfiguration{
				Subnets:        input.Subnets,
				SecurityGroups: input.SecurityGroups,
				AssignPublicIp: ecsTypes.AssignPublicIpDisabled,
			},
		}
	}

	// Capacity provider strategy (mutually exclusive with launch type)
	if input.CapacityProvider != "" {
		ecsInput.CapacityProviderStrategy = []ecsTypes.CapacityProviderStrategyItem{
			{CapacityProvider: aws.String(input.CapacityProvider), Weight: 1},
		}
	} else if input.LaunchType != "" {
		ecsInput.LaunchType = input.LaunchType
	}

	result, err := r.client.RunTask(ctx, ecsInput)
	if err != nil {
		return "", fmt.Errorf("ecs RunTask failed: %w", err)
	}

	if len(result.Tasks) == 0 {
		failureReasons := ""
		for _, f := range result.Failures {
			failureReasons += fmt.Sprintf("%s: %s; ", aws.ToString(f.Arn), aws.ToString(f.Reason))
		}
		return "", fmt.Errorf("no tasks started: %s", failureReasons)
	}

	taskARN := aws.ToString(result.Tasks[0].TaskArn)
	r.logger.Info("runner task started",
		slog.String("scale_set", input.ScaleSetName),
		slog.String("runner_name", input.RunnerName),
		slog.String("ecs_task_id", taskARN),
		slog.String("event", "runner_started"),
	)

	return taskARN, nil
}

// StopTask stops an ECS task with a reason.
func (r *ECSRunner) StopTask(ctx context.Context, taskARN, reason string) error {
	_, err := r.client.StopTask(ctx, &ecs.StopTaskInput{
		Cluster: aws.String(r.cluster),
		Task:    aws.String(taskARN),
		Reason:  aws.String(reason),
	})
	if err != nil {
		return fmt.Errorf("ecs StopTask failed for %s: %w", taskARN, err)
	}
	r.logger.Info("runner task stopped",
		slog.String("ecs_task_id", taskARN),
		slog.String("reason", reason),
		slog.String("event", "runner_stopped"),
	)
	return nil
}
