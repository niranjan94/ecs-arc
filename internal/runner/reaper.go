package runner

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/actions/scaleset"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// ScaleSetClient is the subset of the scaleset API the Reaper uses to
// deregister GitHub runner registrations when the ECS task they back has
// stopped. It is a local copy of the interface shape used by
// internal/scaler, duplicated to avoid an import cycle.
type ScaleSetClient interface {
	GetRunnerByName(ctx context.Context, name string) (*scaleset.RunnerReference, error)
	RemoveRunner(ctx context.Context, runnerID int64) error
}

// Reaper periodically checks for stale runner tasks and stops them.
// It handles two cases:
//   - Tasks stuck in PENDING state beyond the pending timeout
//   - Tasks running longer than the configured max runtime
type Reaper struct {
	client         ECSClient
	ssClient       ScaleSetClient
	cluster        string
	scaleSetName   string
	maxRuntime     time.Duration
	pendingTimeout time.Duration
	state          *State
	logger         *slog.Logger
}

// NewReaper creates a new Reaper.
func NewReaper(
	client ECSClient,
	ssClient ScaleSetClient,
	cluster, scaleSetName string,
	maxRuntime, pendingTimeout time.Duration,
	state *State,
	logger *slog.Logger,
) *Reaper {
	return &Reaper{
		client:         client,
		ssClient:       ssClient,
		cluster:        cluster,
		scaleSetName:   scaleSetName,
		maxRuntime:     maxRuntime,
		pendingTimeout: pendingTimeout,
		state:          state,
		logger:         logger,
	}
}

// Run starts the reaper loop, sweeping on the given interval until the
// context is cancelled.
func (r *Reaper) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if stopped, err := r.Sweep(ctx); err != nil {
				r.logger.Error("reaper sweep failed",
					slog.String("scale_set", r.scaleSetName),
					slog.String("error", err.Error()),
				)
			} else if stopped > 0 {
				r.logger.Info("reaper stopped stale tasks",
					slog.String("scale_set", r.scaleSetName),
					slog.Int("stopped", stopped),
				)
			}
		}
	}
}

// Sweep checks all tasks for this scale set and stops stale ones.
// Returns the number of tasks stopped.
func (r *Reaper) Sweep(ctx context.Context) (int, error) {
	listOutput, err := r.client.ListTasks(ctx, &ecs.ListTasksInput{
		Cluster: aws.String(r.cluster),
	})
	if err != nil {
		return 0, fmt.Errorf("failed to list tasks: %w", err)
	}

	if len(listOutput.TaskArns) == 0 {
		return 0, nil
	}

	descOutput, err := r.client.DescribeTasks(ctx, &ecs.DescribeTasksInput{
		Cluster: aws.String(r.cluster),
		Tasks:   listOutput.TaskArns,
		Include: []ecsTypes.TaskField{ecsTypes.TaskFieldTags},
	})
	if err != nil {
		return 0, fmt.Errorf("failed to describe tasks: %w", err)
	}

	stopped := 0
	now := time.Now()

	for _, task := range descOutput.Tasks {
		if !r.isOurTask(&task) {
			continue
		}

		status := aws.ToString(task.LastStatus)
		age := now.Sub(*task.CreatedAt)

		var reason string
		switch {
		case status == "PENDING" && age > r.pendingTimeout:
			reason = fmt.Sprintf("stuck in PENDING for %s (threshold: %s)", age.Round(time.Second), r.pendingTimeout)
		case status == "RUNNING" && age > r.maxRuntime:
			reason = fmt.Sprintf("exceeded max runtime %s (running: %s)", r.maxRuntime, age.Round(time.Second))
		default:
			continue
		}

		r.logger.Warn("stopping stale runner task",
			slog.String("ecs_task_id", aws.ToString(task.TaskArn)),
			slog.String("status", status),
			slog.String("reason", reason),
			slog.String("event", "runner_stopped"),
		)

		_, err := r.client.StopTask(ctx, &ecs.StopTaskInput{
			Cluster: aws.String(r.cluster),
			Task:    task.TaskArn,
			Reason:  aws.String("ecs-arc: " + reason),
		})
		if err != nil {
			r.logger.Error("failed to stop stale task",
				slog.String("ecs_task_id", aws.ToString(task.TaskArn)),
				slog.String("error", err.Error()),
			)
			continue
		}
		stopped++
	}

	return stopped, nil
}

func (r *Reaper) isOurTask(task *ecsTypes.Task) bool {
	for _, tag := range task.Tags {
		if aws.ToString(tag.Key) == "ecs-arc:scale-set" && aws.ToString(tag.Value) == r.scaleSetName {
			return true
		}
	}
	return false
}
