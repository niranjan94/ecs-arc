// Package scaler implements the listener.Scaler interface for ECS-based
// GitHub Actions runners. It handles scaling decisions, JIT config generation,
// and delegates ECS task management to the runner package.
package scaler

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
	"github.com/google/uuid"
	"github.com/niranjan94/ecs-arc/internal/runner"
	"github.com/niranjan94/ecs-arc/internal/taskdef"
)

// ScaleSetClient is the subset of scaleset.Client that the scaler needs.
type ScaleSetClient interface {
	GenerateJitRunnerConfig(ctx context.Context, setting *scaleset.RunnerScaleSetJitRunnerSetting, scaleSetID int) (*scaleset.RunnerScaleSetJitRunnerConfig, error)
	GetRunnerByName(ctx context.Context, name string) (*scaleset.RunnerReference, error)
	RemoveRunner(ctx context.Context, runnerID int64) error
}

// TaskRunner is the interface for managing ECS runner tasks.
type TaskRunner interface {
	RunTask(ctx context.Context, input runner.RunTaskInput) (string, error)
	StopTask(ctx context.Context, taskARN, reason string) error
}

// ECSScaler implements listener.Scaler for ECS-based runners.
type ECSScaler struct {
	client        ScaleSetClient
	runner        TaskRunner
	state         *runner.State
	scaleSetID    int
	scaleSetName  string
	taskDefFamily string
	config        taskdef.ScaleSetConfig
	logger        *slog.Logger
}

// Verify ECSScaler implements the Scaler interface at compile time.
var _ listener.Scaler = (*ECSScaler)(nil)

// NewECSScaler creates a new ECSScaler.
func NewECSScaler(
	client ScaleSetClient,
	taskRunner TaskRunner,
	scaleSetID int,
	scaleSetName string,
	taskDefFamily string,
	config taskdef.ScaleSetConfig,
	logger *slog.Logger,
) *ECSScaler {
	return &ECSScaler{
		client:        client,
		runner:        taskRunner,
		state:         runner.NewState(),
		scaleSetID:    scaleSetID,
		scaleSetName:  scaleSetName,
		taskDefFamily: taskDefFamily,
		config:        config,
		logger:        logger,
	}
}

// HandleDesiredRunnerCount is called on every poll cycle. It computes the
// target runner count (applying min/max bounds) and scales up if needed.
// Scale-down is handled naturally by ephemeral runners exiting after jobs.
func (s *ECSScaler) HandleDesiredRunnerCount(ctx context.Context, count int) (int, error) {
	target := max(count, s.config.MinRunners)
	target = min(target, s.config.MaxRunners)

	current := s.state.Count()

	if target <= current {
		return current, nil
	}

	scaleUp := target - current
	s.logger.Info("scaling up",
		slog.String("scale_set", s.scaleSetName),
		slog.Int("current", current),
		slog.Int("target", target),
		slog.Int("scale_up", scaleUp),
		slog.String("event", "scale_up"),
	)

	for range scaleUp {
		if err := s.startRunner(ctx); err != nil {
			s.logger.Error("failed to start runner",
				slog.String("scale_set", s.scaleSetName),
				slog.String("error", err.Error()),
			)
			continue
		}
	}

	return s.state.Count(), nil
}

// HandleJobStarted marks a runner as busy when it picks up a job.
func (s *ECSScaler) HandleJobStarted(ctx context.Context, jobInfo *scaleset.JobStarted) error {
	s.logger.Info("job started",
		slog.String("scale_set", s.scaleSetName),
		slog.String("runner_name", jobInfo.RunnerName),
		slog.String("job_id", jobInfo.JobID),
		slog.String("event", "job_started"),
	)
	s.state.MarkBusy(jobInfo.RunnerName)
	return nil
}

// HandleJobCompleted removes a runner from tracking. The ECS task will stop
// on its own since the runner is ephemeral.
func (s *ECSScaler) HandleJobCompleted(ctx context.Context, jobInfo *scaleset.JobCompleted) error {
	s.logger.Info("job completed",
		slog.String("scale_set", s.scaleSetName),
		slog.String("runner_name", jobInfo.RunnerName),
		slog.String("job_id", jobInfo.JobID),
		slog.String("result", jobInfo.Result),
		slog.String("event", "job_completed"),
	)
	s.state.MarkDone(jobInfo.RunnerName)
	return nil
}

func (s *ECSScaler) startRunner(ctx context.Context) error {
	name := fmt.Sprintf("%s-%s", s.scaleSetName, uuid.NewString()[:8])

	jitConfig, err := s.client.GenerateJitRunnerConfig(
		ctx,
		&scaleset.RunnerScaleSetJitRunnerSetting{
			Name:       name,
			WorkFolder: "_work",
		},
		s.scaleSetID,
	)
	if err != nil {
		// Handle runner name collision by deregistering the orphaned runner
		if ref, lookupErr := s.client.GetRunnerByName(ctx, name); lookupErr == nil && ref != nil {
			s.logger.Warn("removing orphaned runner registration",
				slog.String("runner_name", name),
				slog.Int("runner_id", ref.ID),
			)
			if removeErr := s.client.RemoveRunner(ctx, int64(ref.ID)); removeErr != nil {
				return fmt.Errorf("failed to remove orphaned runner %q: %w", name, removeErr)
			}
			// Retry with a new name
			name = fmt.Sprintf("%s-%s", s.scaleSetName, uuid.NewString()[:8])
			jitConfig, err = s.client.GenerateJitRunnerConfig(
				ctx,
				&scaleset.RunnerScaleSetJitRunnerSetting{Name: name, WorkFolder: "_work"},
				s.scaleSetID,
			)
			if err != nil {
				return fmt.Errorf("failed to generate JIT config after retry: %w", err)
			}
		} else {
			return fmt.Errorf("failed to generate JIT config: %w", err)
		}
	}

	taskARN, err := s.runner.RunTask(ctx, runner.RunTaskInput{
		TaskDefinition:   s.taskDefFamily,
		JITConfigEncoded: jitConfig.EncodedJITConfig,
		RunnerName:       name,
		ScaleSetName:     s.scaleSetName,
		Subnets:          s.config.Subnets,
		SecurityGroups:   s.config.SecurityGroups,
		CapacityProvider: s.config.CapacityProvider,
		LaunchType:       s.config.LaunchType,
	})
	if err != nil {
		return fmt.Errorf("failed to run task: %w", err)
	}

	s.state.AddIdle(name, taskARN)
	return nil
}
