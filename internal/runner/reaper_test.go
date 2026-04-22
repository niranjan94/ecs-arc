package runner

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/actions/scaleset"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

func TestReaper_StopsPendingTasks(t *testing.T) {
	now := time.Now()
	stalePendingTime := now.Add(-6 * time.Minute)

	mock := &mockECSClient{
		listTasksOutput: &ecs.ListTasksOutput{
			TaskArns: []string{"arn:stale-pending"},
		},
		describeTaskOutput: &ecs.DescribeTasksOutput{
			Tasks: []ecsTypes.Task{
				{
					TaskArn:    aws.String("arn:stale-pending"),
					LastStatus: aws.String("PENDING"),
					CreatedAt:  &stalePendingTime,
					Tags: []ecsTypes.Tag{
						{Key: aws.String("ecs-arc:scale-set"), Value: aws.String("test-set")},
					},
				},
			},
		},
	}

	r := &Reaper{
		client:         mock,
		ssClient:       nil,
		cluster:        "test-cluster",
		scaleSetName:   "test-set",
		maxRuntime:     6 * time.Hour,
		pendingTimeout: 5 * time.Minute,
		state:          NewState(),
		logger:         slog.Default(),
	}

	stopped, err := r.Sweep(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stopped != 1 {
		t.Errorf("stopped = %d, want 1", stopped)
	}
}

func TestReaper_StopsOverMaxRuntime(t *testing.T) {
	now := time.Now()
	oldRunning := now.Add(-7 * time.Hour)

	mock := &mockECSClient{
		listTasksOutput: &ecs.ListTasksOutput{
			TaskArns: []string{"arn:old-running"},
		},
		describeTaskOutput: &ecs.DescribeTasksOutput{
			Tasks: []ecsTypes.Task{
				{
					TaskArn:    aws.String("arn:old-running"),
					LastStatus: aws.String("RUNNING"),
					CreatedAt:  &oldRunning,
					Tags: []ecsTypes.Tag{
						{Key: aws.String("ecs-arc:scale-set"), Value: aws.String("test-set")},
					},
				},
			},
		},
	}

	r := &Reaper{
		client:         mock,
		ssClient:       nil,
		cluster:        "test-cluster",
		scaleSetName:   "test-set",
		maxRuntime:     6 * time.Hour,
		pendingTimeout: 5 * time.Minute,
		state:          NewState(),
		logger:         slog.Default(),
	}

	stopped, err := r.Sweep(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stopped != 1 {
		t.Errorf("stopped = %d, want 1", stopped)
	}
}

func TestReaper_IgnoresHealthyTasks(t *testing.T) {
	now := time.Now()
	recentRunning := now.Add(-10 * time.Minute)

	mock := &mockECSClient{
		listTasksOutput: &ecs.ListTasksOutput{
			TaskArns: []string{"arn:healthy"},
		},
		describeTaskOutput: &ecs.DescribeTasksOutput{
			Tasks: []ecsTypes.Task{
				{
					TaskArn:    aws.String("arn:healthy"),
					LastStatus: aws.String("RUNNING"),
					CreatedAt:  &recentRunning,
					Tags: []ecsTypes.Tag{
						{Key: aws.String("ecs-arc:scale-set"), Value: aws.String("test-set")},
					},
				},
			},
		},
	}

	r := &Reaper{
		client:         mock,
		ssClient:       nil,
		cluster:        "test-cluster",
		scaleSetName:   "test-set",
		maxRuntime:     6 * time.Hour,
		pendingTimeout: 5 * time.Minute,
		state:          NewState(),
		logger:         slog.Default(),
	}

	stopped, err := r.Sweep(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stopped != 0 {
		t.Errorf("stopped = %d, want 0", stopped)
	}
}

type fakeReaperScaleSetClient struct {
	getByNameFn    func(ctx context.Context, name string) (*scaleset.RunnerReference, error)
	removeFn       func(ctx context.Context, id int64) error
	getByNameCalls []string
	removeCalls    []int64
}

func (f *fakeReaperScaleSetClient) GetRunnerByName(ctx context.Context, name string) (*scaleset.RunnerReference, error) {
	f.getByNameCalls = append(f.getByNameCalls, name)
	if f.getByNameFn != nil {
		return f.getByNameFn(ctx, name)
	}
	return nil, nil
}

func (f *fakeReaperScaleSetClient) RemoveRunner(ctx context.Context, id int64) error {
	f.removeCalls = append(f.removeCalls, id)
	if f.removeFn != nil {
		return f.removeFn(ctx, id)
	}
	return nil
}

func TestReaper_DeregistersStoppedRunner(t *testing.T) {
	now := time.Now()
	recent := now.Add(-30 * time.Second)

	ecsMock := &mockECSClient{
		listTasksOutput: &ecs.ListTasksOutput{TaskArns: []string{"arn:stopped"}},
		describeTaskOutput: &ecs.DescribeTasksOutput{
			Tasks: []ecsTypes.Task{
				{
					TaskArn:    aws.String("arn:stopped"),
					LastStatus: aws.String("STOPPED"),
					CreatedAt:  &recent,
					Tags: []ecsTypes.Tag{
						{Key: aws.String("ecs-arc:scale-set"), Value: aws.String("test-set")},
						{Key: aws.String("ecs-arc:runner-name"), Value: aws.String("runner-abc")},
					},
				},
			},
		},
	}
	ssMock := &fakeReaperScaleSetClient{
		getByNameFn: func(_ context.Context, _ string) (*scaleset.RunnerReference, error) {
			return &scaleset.RunnerReference{ID: 42, Name: "runner-abc"}, nil
		},
	}
	state := NewState()

	r := &Reaper{
		client:         ecsMock,
		ssClient:       ssMock,
		cluster:        "test-cluster",
		scaleSetName:   "test-set",
		maxRuntime:     6 * time.Hour,
		pendingTimeout: 5 * time.Minute,
		state:          state,
		logger:         slog.Default(),
	}

	_, err := r.Sweep(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := ssMock.getByNameCalls; len(got) != 1 || got[0] != "runner-abc" {
		t.Errorf("GetRunnerByName calls = %v, want [runner-abc]", got)
	}
	if got := ssMock.removeCalls; len(got) != 1 || got[0] != 42 {
		t.Errorf("RemoveRunner calls = %v, want [42]", got)
	}
	if !state.IsDeregistered("runner-abc") {
		t.Error("state should mark runner-abc deregistered after successful removal")
	}
}

func TestReaper_SkipsAlreadyDeregistered(t *testing.T) {
	state := NewState()
	state.MarkDeregistered("runner-abc")

	now := time.Now()
	recent := now.Add(-30 * time.Second)
	ecsMock := &mockECSClient{
		listTasksOutput: &ecs.ListTasksOutput{TaskArns: []string{"arn:stopped"}},
		describeTaskOutput: &ecs.DescribeTasksOutput{
			Tasks: []ecsTypes.Task{
				{
					TaskArn:    aws.String("arn:stopped"),
					LastStatus: aws.String("STOPPED"),
					CreatedAt:  &recent,
					Tags: []ecsTypes.Tag{
						{Key: aws.String("ecs-arc:scale-set"), Value: aws.String("test-set")},
						{Key: aws.String("ecs-arc:runner-name"), Value: aws.String("runner-abc")},
					},
				},
			},
		},
	}
	ssMock := &fakeReaperScaleSetClient{}

	r := &Reaper{
		client: ecsMock, ssClient: ssMock, state: state,
		cluster: "c", scaleSetName: "test-set",
		maxRuntime: time.Hour, pendingTimeout: 5 * time.Minute,
		logger: slog.Default(),
	}

	if _, err := r.Sweep(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ssMock.getByNameCalls) != 0 {
		t.Errorf("GetRunnerByName should not be called when already deregistered; got %v", ssMock.getByNameCalls)
	}
}

func TestReaper_StoppedRunner_NotFound_MarksDeregistered(t *testing.T) {
	state := NewState()
	now := time.Now()
	recent := now.Add(-30 * time.Second)
	ecsMock := &mockECSClient{
		listTasksOutput: &ecs.ListTasksOutput{TaskArns: []string{"arn:stopped"}},
		describeTaskOutput: &ecs.DescribeTasksOutput{
			Tasks: []ecsTypes.Task{
				{
					TaskArn:    aws.String("arn:stopped"),
					LastStatus: aws.String("STOPPED"),
					CreatedAt:  &recent,
					Tags: []ecsTypes.Tag{
						{Key: aws.String("ecs-arc:scale-set"), Value: aws.String("test-set")},
						{Key: aws.String("ecs-arc:runner-name"), Value: aws.String("runner-abc")},
					},
				},
			},
		},
	}
	ssMock := &fakeReaperScaleSetClient{
		getByNameFn: func(_ context.Context, _ string) (*scaleset.RunnerReference, error) {
			return nil, nil
		},
	}
	r := &Reaper{
		client: ecsMock, ssClient: ssMock, state: state,
		cluster: "c", scaleSetName: "test-set",
		maxRuntime: time.Hour, pendingTimeout: 5 * time.Minute,
		logger: slog.Default(),
	}

	if _, err := r.Sweep(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ssMock.removeCalls) != 0 {
		t.Errorf("RemoveRunner should not be called when lookup returns nil; got %v", ssMock.removeCalls)
	}
	if !state.IsDeregistered("runner-abc") {
		t.Error("nil lookup should still mark state as deregistered")
	}
}

func TestReaper_StoppedRunner_JobStillRunning_DoesNotMark(t *testing.T) {
	state := NewState()
	now := time.Now()
	recent := now.Add(-30 * time.Second)
	ecsMock := &mockECSClient{
		listTasksOutput: &ecs.ListTasksOutput{TaskArns: []string{"arn:stopped"}},
		describeTaskOutput: &ecs.DescribeTasksOutput{
			Tasks: []ecsTypes.Task{{
				TaskArn:    aws.String("arn:stopped"),
				LastStatus: aws.String("STOPPED"),
				CreatedAt:  &recent,
				Tags: []ecsTypes.Tag{
					{Key: aws.String("ecs-arc:scale-set"), Value: aws.String("test-set")},
					{Key: aws.String("ecs-arc:runner-name"), Value: aws.String("runner-abc")},
				},
			}},
		},
	}
	ssMock := &fakeReaperScaleSetClient{
		getByNameFn: func(_ context.Context, _ string) (*scaleset.RunnerReference, error) {
			return &scaleset.RunnerReference{ID: 7, Name: "runner-abc"}, nil
		},
		removeFn: func(_ context.Context, _ int64) error {
			return scaleset.JobStillRunningError
		},
	}
	r := &Reaper{
		client: ecsMock, ssClient: ssMock, state: state,
		cluster: "c", scaleSetName: "test-set",
		maxRuntime: time.Hour, pendingTimeout: 5 * time.Minute,
		logger: slog.Default(),
	}

	if _, err := r.Sweep(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.IsDeregistered("runner-abc") {
		t.Error("JobStillRunning should not mark state deregistered")
	}
}

func TestReaper_StoppedRunner_MissingTag_NoAPICalls(t *testing.T) {
	state := NewState()
	now := time.Now()
	recent := now.Add(-30 * time.Second)
	ecsMock := &mockECSClient{
		listTasksOutput: &ecs.ListTasksOutput{TaskArns: []string{"arn:legacy"}},
		describeTaskOutput: &ecs.DescribeTasksOutput{
			Tasks: []ecsTypes.Task{{
				TaskArn:    aws.String("arn:legacy"),
				LastStatus: aws.String("STOPPED"),
				CreatedAt:  &recent,
				Tags: []ecsTypes.Tag{
					{Key: aws.String("ecs-arc:scale-set"), Value: aws.String("test-set")},
				},
			}},
		},
	}
	ssMock := &fakeReaperScaleSetClient{}
	r := &Reaper{
		client: ecsMock, ssClient: ssMock, state: state,
		cluster: "c", scaleSetName: "test-set",
		maxRuntime: time.Hour, pendingTimeout: 5 * time.Minute,
		logger: slog.Default(),
	}

	if _, err := r.Sweep(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ssMock.getByNameCalls) != 0 || len(ssMock.removeCalls) != 0 {
		t.Errorf("no API calls expected; got get=%v remove=%v", ssMock.getByNameCalls, ssMock.removeCalls)
	}
}
