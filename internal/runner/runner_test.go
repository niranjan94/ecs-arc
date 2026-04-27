package runner

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

type mockECSClient struct {
	runTaskOutput      *ecs.RunTaskOutput
	runTaskErr         error
	stopTaskErr        error
	listTasksOutput    *ecs.ListTasksOutput
	describeTaskOutput *ecs.DescribeTasksOutput

	lastRunTaskInput *ecs.RunTaskInput
}

func (m *mockECSClient) RunTask(_ context.Context, input *ecs.RunTaskInput, _ ...func(*ecs.Options)) (*ecs.RunTaskOutput, error) {
	m.lastRunTaskInput = input
	return m.runTaskOutput, m.runTaskErr
}

func (m *mockECSClient) StopTask(_ context.Context, _ *ecs.StopTaskInput, _ ...func(*ecs.Options)) (*ecs.StopTaskOutput, error) {
	return nil, m.stopTaskErr
}

func (m *mockECSClient) ListTasks(_ context.Context, _ *ecs.ListTasksInput, _ ...func(*ecs.Options)) (*ecs.ListTasksOutput, error) {
	return m.listTasksOutput, nil
}

func (m *mockECSClient) DescribeTasks(_ context.Context, _ *ecs.DescribeTasksInput, _ ...func(*ecs.Options)) (*ecs.DescribeTasksOutput, error) {
	return m.describeTaskOutput, nil
}

func TestRunTask_SetsJITConfigEnvVar(t *testing.T) {
	mock := &mockECSClient{
		runTaskOutput: &ecs.RunTaskOutput{
			Tasks: []ecsTypes.Task{
				{TaskArn: aws.String("arn:aws:ecs:us-east-1:123:task/my-cluster/abc123")},
			},
		},
	}

	r := &ECSRunner{client: mock, cluster: "my-cluster", logger: slog.Default()}
	input := RunTaskInput{
		TaskDefinition:   "runner-small",
		JITConfigEncoded: "base64-jit-config",
		RunnerName:       "runner-abc",
		ScaleSetName:     "prod-runner-small",
		Subnets:          []string{"subnet-aaa"},
		SecurityGroups:   []string{"sg-xxx"},
	}

	taskARN, err := r.RunTask(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taskARN == "" {
		t.Fatal("expected non-empty task ARN")
	}

	overrides := mock.lastRunTaskInput.Overrides
	if overrides == nil || len(overrides.ContainerOverrides) == 0 {
		t.Fatal("expected container overrides")
	}
	envVars := overrides.ContainerOverrides[0].Environment
	found := false
	for _, env := range envVars {
		if *env.Name == "ACTIONS_RUNNER_INPUT_JITCONFIG" && *env.Value == "base64-jit-config" {
			found = true
		}
	}
	if !found {
		t.Error("ACTIONS_RUNNER_INPUT_JITCONFIG env var not found in container overrides")
	}
}

func TestRunTask_WithCapacityProvider(t *testing.T) {
	mock := &mockECSClient{
		runTaskOutput: &ecs.RunTaskOutput{
			Tasks: []ecsTypes.Task{
				{TaskArn: aws.String("arn:aws:ecs:us-east-1:123:task/my-cluster/abc123")},
			},
		},
	}

	r := &ECSRunner{client: mock, cluster: "my-cluster", logger: slog.Default()}
	input := RunTaskInput{
		TaskDefinition:   "runner-small",
		JITConfigEncoded: "jit",
		RunnerName:       "runner-abc",
		ScaleSetName:     "prod-runner-small",
		Subnets:          []string{"subnet-aaa"},
		SecurityGroups:   []string{"sg-xxx"},
		CapacityProvider: "my-cp",
	}

	_, err := r.RunTask(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.lastRunTaskInput.CapacityProviderStrategy == nil {
		t.Fatal("expected capacity provider strategy")
	}
	if *mock.lastRunTaskInput.CapacityProviderStrategy[0].CapacityProvider != "my-cp" {
		t.Error("wrong capacity provider")
	}
}

func TestRunTask_NoNetworkConfigForExternal(t *testing.T) {
	mock := &mockECSClient{
		runTaskOutput: &ecs.RunTaskOutput{
			Tasks: []ecsTypes.Task{
				{TaskArn: aws.String("arn:aws:ecs:us-east-1:123:task/my-cluster/abc123")},
			},
		},
	}

	r := &ECSRunner{client: mock, cluster: "my-cluster", logger: slog.Default()}
	input := RunTaskInput{
		TaskDefinition:   "runner-small",
		JITConfigEncoded: "jit",
		RunnerName:       "runner-abc",
		ScaleSetName:     "prod-runner-small",
		LaunchType:       ecsTypes.LaunchTypeExternal,
	}

	_, err := r.RunTask(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.lastRunTaskInput.NetworkConfiguration != nil {
		t.Error("expected no network configuration for EXTERNAL mode")
	}
	if mock.lastRunTaskInput.LaunchType != ecsTypes.LaunchTypeExternal {
		t.Errorf("expected launch type EXTERNAL, got %q", mock.lastRunTaskInput.LaunchType)
	}
}

func TestRunTask_WrapsTransientAPIError(t *testing.T) {
	mock := &mockECSClient{
		runTaskErr: &fakeAPIError{code: "ThrottlingException"},
	}
	r := &ECSRunner{client: mock, cluster: "my-cluster", logger: slog.Default()}
	_, err := r.RunTask(context.Background(), RunTaskInput{
		TaskDefinition:   "runner-small",
		JITConfigEncoded: "jit",
		RunnerName:       "runner-x",
		ScaleSetName:     "ss",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrTransientCapacity) {
		t.Errorf("expected ErrTransientCapacity, got %v", err)
	}
	if !strings.Contains(err.Error(), "ThrottlingException") {
		t.Errorf("expected underlying error visible in message, got %q", err.Error())
	}
}

func TestRunTask_DoesNotWrapNonTransientAPIError(t *testing.T) {
	mock := &mockECSClient{
		runTaskErr: &fakeAPIError{code: "InvalidParameterException"},
	}
	r := &ECSRunner{client: mock, cluster: "my-cluster", logger: slog.Default()}
	_, err := r.RunTask(context.Background(), RunTaskInput{
		TaskDefinition: "runner-small", JITConfigEncoded: "jit",
		RunnerName: "runner-x", ScaleSetName: "ss",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, ErrTransientCapacity) {
		t.Errorf("did not expect ErrTransientCapacity, got %v", err)
	}
}

func TestRunTask_WrapsTransientFailureReason(t *testing.T) {
	mock := &mockECSClient{
		runTaskOutput: &ecs.RunTaskOutput{
			Tasks: nil,
			Failures: []ecsTypes.Failure{
				{Arn: aws.String("a"), Reason: aws.String("RESOURCE:MEMORY")},
			},
		},
	}
	r := &ECSRunner{client: mock, cluster: "my-cluster", logger: slog.Default()}
	_, err := r.RunTask(context.Background(), RunTaskInput{
		TaskDefinition: "runner-small", JITConfigEncoded: "jit",
		RunnerName: "runner-x", ScaleSetName: "ss",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrTransientCapacity) {
		t.Errorf("expected ErrTransientCapacity, got %v", err)
	}
	if !strings.Contains(err.Error(), "RESOURCE:MEMORY") {
		t.Errorf("expected reason visible in message, got %q", err.Error())
	}
}

func TestRunTask_DoesNotWrapNonTransientFailureReason(t *testing.T) {
	mock := &mockECSClient{
		runTaskOutput: &ecs.RunTaskOutput{
			Tasks: nil,
			Failures: []ecsTypes.Failure{
				{Arn: aws.String("a"), Reason: aws.String("Task failed ELB health checks")},
			},
		},
	}
	r := &ECSRunner{client: mock, cluster: "my-cluster", logger: slog.Default()}
	_, err := r.RunTask(context.Background(), RunTaskInput{
		TaskDefinition: "runner-small", JITConfigEncoded: "jit",
		RunnerName: "runner-x", ScaleSetName: "ss",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, ErrTransientCapacity) {
		t.Errorf("did not expect ErrTransientCapacity, got %v", err)
	}
}
