package scaler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/actions/scaleset"
	"github.com/niranjan94/ecs-arc/internal/runner"
	"github.com/niranjan94/ecs-arc/internal/taskdef"
)

type mockScaleSetClient struct {
	jitConfig     *scaleset.RunnerScaleSetJitRunnerConfig
	jitErr        error
	removedRunner int64
	removeCalls   []int64
	removeErr     error
	getRunnerRef  *scaleset.RunnerReference
	getRunnerErr  error
}

func (m *mockScaleSetClient) GenerateJitRunnerConfig(_ context.Context, _ *scaleset.RunnerScaleSetJitRunnerSetting, _ int) (*scaleset.RunnerScaleSetJitRunnerConfig, error) {
	return m.jitConfig, m.jitErr
}

func (m *mockScaleSetClient) GetRunnerByName(_ context.Context, _ string) (*scaleset.RunnerReference, error) {
	return m.getRunnerRef, m.getRunnerErr
}

func (m *mockScaleSetClient) RemoveRunner(_ context.Context, runnerID int64) error {
	m.removedRunner = runnerID
	m.removeCalls = append(m.removeCalls, runnerID)
	return m.removeErr
}

type mockTaskRunner struct {
	runTaskCalls  []runner.RunTaskInput
	runTaskErr    error
	stopTaskCalls []string
	stopTaskErr   error
}

func (m *mockTaskRunner) RunTask(_ context.Context, input runner.RunTaskInput) (string, error) {
	m.runTaskCalls = append(m.runTaskCalls, input)
	return fmt.Sprintf("arn:aws:ecs:task/%d", len(m.runTaskCalls)), m.runTaskErr
}

func (m *mockTaskRunner) StopTask(_ context.Context, taskARN, _ string) error {
	m.stopTaskCalls = append(m.stopTaskCalls, taskARN)
	return m.stopTaskErr
}

func TestHandleDesiredRunnerCount_ScaleUp(t *testing.T) {
	mockClient := &mockScaleSetClient{
		jitConfig: &scaleset.RunnerScaleSetJitRunnerConfig{
			Runner:           &scaleset.RunnerReference{ID: 1, Name: "test-runner"},
			EncodedJITConfig: "base64-jit",
		},
	}
	mockRunner := &mockTaskRunner{}

	s := &ECSScaler{
		client:       mockClient,
		runner:       mockRunner,
		state:        runner.NewState(),
		scaleSetID:   1,
		scaleSetName: "prod-runner-small",
		config: taskdef.ScaleSetConfig{
			MaxRunners:     10,
			MinRunners:     0,
			Subnets:        []string{"subnet-aaa"},
			SecurityGroups: []string{"sg-xxx"},
		},
		taskDefFamily: "runner-small",
		logger:        slog.Default(),
	}

	count, err := s.HandleDesiredRunnerCount(context.Background(), 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
	if len(mockRunner.runTaskCalls) != 3 {
		t.Errorf("RunTask called %d times, want 3", len(mockRunner.runTaskCalls))
	}
}

func TestHandleDesiredRunnerCount_RespectsMaxRunners(t *testing.T) {
	mockClient := &mockScaleSetClient{
		jitConfig: &scaleset.RunnerScaleSetJitRunnerConfig{
			Runner:           &scaleset.RunnerReference{ID: 1, Name: "test-runner"},
			EncodedJITConfig: "base64-jit",
		},
	}
	mockRunner := &mockTaskRunner{}

	s := &ECSScaler{
		client:       mockClient,
		runner:       mockRunner,
		state:        runner.NewState(),
		scaleSetID:   1,
		scaleSetName: "prod-runner-small",
		config: taskdef.ScaleSetConfig{
			MaxRunners:     2,
			MinRunners:     0,
			Subnets:        []string{"subnet-aaa"},
			SecurityGroups: []string{"sg-xxx"},
		},
		taskDefFamily: "runner-small",
		logger:        slog.Default(),
	}

	count, err := s.HandleDesiredRunnerCount(context.Background(), 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2 (max)", count)
	}
	if len(mockRunner.runTaskCalls) != 2 {
		t.Errorf("RunTask called %d times, want 2", len(mockRunner.runTaskCalls))
	}
}

func TestHandleDesiredRunnerCount_MinRunners(t *testing.T) {
	mockClient := &mockScaleSetClient{
		jitConfig: &scaleset.RunnerScaleSetJitRunnerConfig{
			Runner:           &scaleset.RunnerReference{ID: 1, Name: "test-runner"},
			EncodedJITConfig: "base64-jit",
		},
	}
	mockRunner := &mockTaskRunner{}

	s := &ECSScaler{
		client:       mockClient,
		runner:       mockRunner,
		state:        runner.NewState(),
		scaleSetID:   1,
		scaleSetName: "prod-runner-small",
		config: taskdef.ScaleSetConfig{
			MaxRunners:     10,
			MinRunners:     3,
			Subnets:        []string{"subnet-aaa"},
			SecurityGroups: []string{"sg-xxx"},
		},
		taskDefFamily: "runner-small",
		logger:        slog.Default(),
	}

	count, err := s.HandleDesiredRunnerCount(context.Background(), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3 (min)", count)
	}
}

func TestHandleDesiredRunnerCount_NoScaleNeeded(t *testing.T) {
	mockClient := &mockScaleSetClient{}
	mockRunner := &mockTaskRunner{}

	s := &ECSScaler{
		client:       mockClient,
		runner:       mockRunner,
		state:        runner.NewState(),
		scaleSetID:   1,
		scaleSetName: "prod-runner-small",
		config: taskdef.ScaleSetConfig{
			MaxRunners: 10,
			MinRunners: 0,
		},
		taskDefFamily: "runner-small",
		logger:        slog.Default(),
	}

	s.state.AddIdle("runner-1", "arn-1")
	s.state.AddIdle("runner-2", "arn-2")

	count, err := s.HandleDesiredRunnerCount(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2 (already have enough)", count)
	}
	if len(mockRunner.runTaskCalls) != 0 {
		t.Errorf("RunTask called %d times, want 0", len(mockRunner.runTaskCalls))
	}
}

func TestHandleJobStarted(t *testing.T) {
	s := &ECSScaler{
		state:  runner.NewState(),
		logger: slog.Default(),
	}
	s.state.AddIdle("test-runner", "arn-1")

	err := s.HandleJobStarted(context.Background(), &scaleset.JobStarted{
		RunnerName:     "test-runner",
		JobMessageBase: scaleset.JobMessageBase{JobID: "job-1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.state.IdleCount() != 0 {
		t.Error("runner should have been marked busy")
	}
}

func TestHandleJobCompleted(t *testing.T) {
	s := &ECSScaler{
		client: &mockScaleSetClient{},
		state:  runner.NewState(),
		runner: &mockTaskRunner{},
		logger: slog.Default(),
	}
	s.state.AddIdle("test-runner", "arn-1")
	s.state.MarkBusy("test-runner")

	err := s.HandleJobCompleted(context.Background(), &scaleset.JobCompleted{
		RunnerName:     "test-runner",
		JobMessageBase: scaleset.JobMessageBase{JobID: "job-1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.state.Count() != 0 {
		t.Error("runner should have been removed from tracking")
	}
}

func TestHandleJobCompleted_CallsRemoveRunner(t *testing.T) {
	mockClient := &mockScaleSetClient{}
	s := &ECSScaler{
		client:       mockClient,
		runner:       &mockTaskRunner{},
		state:        runner.NewState(),
		scaleSetID:   1,
		scaleSetName: "prod-runner-small",
		logger:       slog.Default(),
	}
	s.state.AddIdle("test-runner", "arn-1")
	s.state.MarkBusy("test-runner")

	err := s.HandleJobCompleted(context.Background(), &scaleset.JobCompleted{
		RunnerID:       99,
		RunnerName:     "test-runner",
		JobMessageBase: scaleset.JobMessageBase{JobID: "job-1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := mockClient.removeCalls; len(got) != 1 || got[0] != 99 {
		t.Errorf("RemoveRunner calls = %v, want [99]", got)
	}
	if !s.state.IsDeregistered("test-runner") {
		t.Error("state should mark test-runner deregistered")
	}
}

func TestHandleJobCompleted_NotFound_MarksDeregistered(t *testing.T) {
	mockClient := &mockScaleSetClient{removeErr: scaleset.RunnerNotFoundError}
	s := &ECSScaler{
		client: mockClient,
		runner: &mockTaskRunner{},
		state:  runner.NewState(),
		logger: slog.Default(),
	}

	err := s.HandleJobCompleted(context.Background(), &scaleset.JobCompleted{
		RunnerID:   5,
		RunnerName: "runner-x",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s.state.IsDeregistered("runner-x") {
		t.Error("404 should be treated as success for dedup")
	}
}

func TestHandleJobCompleted_JobStillRunning_DoesNotMark(t *testing.T) {
	mockClient := &mockScaleSetClient{removeErr: scaleset.JobStillRunningError}
	s := &ECSScaler{
		client: mockClient,
		runner: &mockTaskRunner{},
		state:  runner.NewState(),
		logger: slog.Default(),
	}

	err := s.HandleJobCompleted(context.Background(), &scaleset.JobCompleted{
		RunnerID:   5,
		RunnerName: "runner-x",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.state.IsDeregistered("runner-x") {
		t.Error("JobStillRunning should not mark state deregistered")
	}
}

func TestHandleJobCompleted_OtherError_StateUnchanged(t *testing.T) {
	mockClient := &mockScaleSetClient{removeErr: errors.New("boom")}
	s := &ECSScaler{
		client: mockClient,
		runner: &mockTaskRunner{},
		state:  runner.NewState(),
		logger: slog.Default(),
	}
	s.state.AddIdle("runner-x", "arn-1")
	s.state.MarkBusy("runner-x")

	err := s.HandleJobCompleted(context.Background(), &scaleset.JobCompleted{
		RunnerID:   5,
		RunnerName: "runner-x",
	})
	if err != nil {
		t.Fatalf("HandleJobCompleted should not propagate inner errors: %v", err)
	}
	if s.state.IsDeregistered("runner-x") {
		t.Error("generic error must not mark deregistered")
	}
	if s.state.Count() != 0 {
		t.Error("MarkDone should still have happened regardless of deregister outcome")
	}
}
