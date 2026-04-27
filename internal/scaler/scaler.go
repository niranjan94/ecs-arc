// Package scaler implements the listener.Scaler interface for ECS-based
// GitHub Actions runners. It handles scaling decisions, JIT config generation,
// and delegates ECS task management to the runner package.
package scaler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

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

	backoffMu        sync.Mutex
	capacityRetryAt  time.Time
	capacityFailures int
	nowFn            func() time.Time // unexported test seam; nil in prod -> time.Now
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
	state *runner.State,
	logger *slog.Logger,
) *ECSScaler {
	return &ECSScaler{
		client:        client,
		runner:        taskRunner,
		state:         state,
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

	if retryAt, blocked := s.capacityBackoffActive(); blocked {
		s.logger.Info("scale-up deferred: ECS capacity backoff active",
			slog.String("scale_set", s.scaleSetName),
			slog.Int("current", current),
			slog.Int("target", target),
			slog.Time("retry_at", retryAt),
			slog.String("event", "scale_up_deferred"),
		)
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
		err := s.startRunner(ctx)
		if err == nil {
			s.resetCapacityBackoff()
			continue
		}
		if errors.Is(err, runner.ErrTransientCapacity) {
			wait := s.recordCapacityFailure()
			s.logger.Warn("ECS capacity unavailable, backing off",
				slog.String("scale_set", s.scaleSetName),
				slog.Int("attempted_target", target),
				slog.Int("started", s.state.Count()),
				slog.Duration("backoff", wait),
				slog.String("error", err.Error()),
				slog.String("event", "scale_up_capacity_backoff"),
			)
			break
		}
		s.logger.Error("failed to start runner",
			slog.String("scale_set", s.scaleSetName),
			slog.String("error", err.Error()),
		)
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
	s.deregisterCompletedRunner(ctx, jobInfo.RunnerName, int64(jobInfo.RunnerID))
	return nil
}

func (s *ECSScaler) deregisterCompletedRunner(ctx context.Context, name string, id int64) {
	if name == "" || id == 0 {
		return
	}
	if s.state.IsDeregistered(name) {
		return
	}
	if err := s.client.RemoveRunner(ctx, id); err != nil {
		if errors.Is(err, scaleset.RunnerNotFoundError) {
			s.state.MarkDeregistered(name)
			return
		}
		if errors.Is(err, scaleset.JobStillRunningError) {
			s.logger.Info("GitHub still reports runner busy on job completion, skipping eager deregister",
				slog.String("runner_name", name),
				slog.Int64("runner_id", id),
			)
			return
		}
		s.logger.Warn("eager runner deregister failed",
			slog.String("runner_name", name),
			slog.Int64("runner_id", id),
			slog.String("error", err.Error()),
		)
		return
	}
	s.logger.Info("deregistered runner on job completion",
		slog.String("runner_name", name),
		slog.Int64("runner_id", id),
		slog.String("event", "runner_deregistered"),
	)
	s.state.MarkDeregistered(name)
}

// now returns the current time, using nowFn if injected (for tests) or
// time.Now otherwise.
func (s *ECSScaler) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

// capacityBackoffSchedule lists the base wait per consecutive transient-
// capacity failure. Index 0 = first failure. Calls beyond the slice length
// reuse the last entry (cap).
var capacityBackoffSchedule = []time.Duration{
	15 * time.Second,
	30 * time.Second,
	60 * time.Second,
	120 * time.Second,
	300 * time.Second,
}

// capacityBackoffActive reports whether the scaler is currently inside a
// capacity-backoff window. The retry time is returned for log fields.
func (s *ECSScaler) capacityBackoffActive() (time.Time, bool) {
	s.backoffMu.Lock()
	defer s.backoffMu.Unlock()
	if s.capacityRetryAt.IsZero() {
		return time.Time{}, false
	}
	if s.now().Before(s.capacityRetryAt) {
		return s.capacityRetryAt, true
	}
	return s.capacityRetryAt, false
}

// recordCapacityFailure increments the consecutive-failure counter and
// schedules the next allowed retry. Returns the wait duration applied
// (after jitter) for logging.
func (s *ECSScaler) recordCapacityFailure() time.Duration {
	s.backoffMu.Lock()
	defer s.backoffMu.Unlock()
	s.capacityFailures++
	idx := s.capacityFailures - 1
	if idx >= len(capacityBackoffSchedule) {
		idx = len(capacityBackoffSchedule) - 1
	}
	base := capacityBackoffSchedule[idx]
	// Uniform ±20% jitter.
	jitterFactor := 0.8 + rand.Float64()*0.4
	wait := time.Duration(float64(base) * jitterFactor)
	s.capacityRetryAt = s.now().Add(wait)
	return wait
}

// resetCapacityBackoff clears the failure counter and retry timestamp.
// Called after a successful scale-up.
func (s *ECSScaler) resetCapacityBackoff() {
	s.backoffMu.Lock()
	defer s.backoffMu.Unlock()
	s.capacityFailures = 0
	s.capacityRetryAt = time.Time{}
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
