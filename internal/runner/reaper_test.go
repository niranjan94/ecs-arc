package runner

import (
	"context"
	"log/slog"
	"testing"
	"time"

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
		cluster:        "test-cluster",
		scaleSetName:   "test-set",
		maxRuntime:     6 * time.Hour,
		pendingTimeout: 5 * time.Minute,
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
		cluster:        "test-cluster",
		scaleSetName:   "test-set",
		maxRuntime:     6 * time.Hour,
		pendingTimeout: 5 * time.Minute,
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
		cluster:        "test-cluster",
		scaleSetName:   "test-set",
		maxRuntime:     6 * time.Hour,
		pendingTimeout: 5 * time.Minute,
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
