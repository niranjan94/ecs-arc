// Package controller is the top-level orchestrator for the ecs-arc controller.
// It wires together the scaleset client, listener, scaler, and runner for
// each configured scale set, managing their lifecycles as goroutines.
package controller

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/niranjan94/ecs-arc/internal/config"
	"github.com/niranjan94/ecs-arc/internal/reconciler"
	"github.com/niranjan94/ecs-arc/internal/runner"
	scalerPkg "github.com/niranjan94/ecs-arc/internal/scaler"
	"github.com/niranjan94/ecs-arc/internal/taskdef"
	"github.com/niranjan94/ecs-arc/internal/tomlcfg"
)

// ManagedLabelName is the reserved system label ecs-arc injects on every
// scale set it manages. Presence of this label is the marker used by the
// startup sweep and runtime deletion path to distinguish ecs-arc-owned
// scale sets from foreign ones in the same runner group.
const ManagedLabelName = "ecs-arc.managed"

func injectManagedLabel(rss *scaleset.RunnerScaleSet) {
	if hasManagedLabel(rss.Labels) {
		return
	}
	rss.Labels = append(rss.Labels, scaleset.Label{Name: ManagedLabelName, Type: "System"})
}

func hasManagedLabel(labels []scaleset.Label) bool {
	for _, l := range labels {
		if l.Name == ManagedLabelName {
			return true
		}
	}
	return false
}

// ScaleSetClient is the subset of *scaleset.Client the controller needs. It
// exists so tests can provide a fake implementation. It is a superset of the
// methods used directly by the controller plus those forwarded to the scaler
// (see scaler.ScaleSetClient).
type ScaleSetClient interface {
	CreateRunnerScaleSet(ctx context.Context, rss *scaleset.RunnerScaleSet) (*scaleset.RunnerScaleSet, error)
	GetRunnerScaleSet(ctx context.Context, runnerGroupID int, name string) (*scaleset.RunnerScaleSet, error)
	UpdateRunnerScaleSet(ctx context.Context, id int, rss *scaleset.RunnerScaleSet) (*scaleset.RunnerScaleSet, error)
	DeleteRunnerScaleSet(ctx context.Context, id int) error
	ListRunnerScaleSets(ctx context.Context, runnerGroupID int) ([]scaleset.RunnerScaleSet, error)
	MessageSessionClient(ctx context.Context, scaleSetID int, owner string, options ...scaleset.HTTPOption) (*scaleset.MessageSessionClient, error)
	GenerateJitRunnerConfig(ctx context.Context, setting *scaleset.RunnerScaleSetJitRunnerSetting, scaleSetID int) (*scaleset.RunnerScaleSetJitRunnerConfig, error)
	GetRunnerByName(ctx context.Context, name string) (*scaleset.RunnerReference, error)
	RemoveRunner(ctx context.Context, runnerID int64) error
}

// Controller manages the lifecycle of all scale set goroutines.
type Controller struct {
	cfg       *config.Config
	ecsClient *ecs.Client
	ssmClient reconciler.SSMClient
	logger    *slog.Logger
}

// New creates a new Controller.
func New(cfg *config.Config, ecsClient *ecs.Client, ssmClient reconciler.SSMClient, logger *slog.Logger) *Controller {
	return &Controller{
		cfg:       cfg,
		ecsClient: ecsClient,
		ssmClient: ssmClient,
		logger:    logger,
	}
}

// Run starts the reconciler and event-driven scale set management loop.
// It blocks until the context is cancelled.
func (c *Controller) Run(ctx context.Context) error {
	scalesetClient, err := scaleset.NewClientWithGitHubApp(scaleset.ClientWithGitHubAppConfig{
		GitHubConfigURL: c.cfg.GitHubConfigURL,
		GitHubAppAuth: scaleset.GitHubAppAuth{
			ClientID:       c.cfg.GitHubAppClientID,
			InstallationID: c.cfg.GitHubAppInstallationID,
			PrivateKey:     c.cfg.GitHubAppPrivateKey,
		},
		SystemInfo: scaleset.SystemInfo{
			System:    "ecs-arc",
			Subsystem: "controller",
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create scaleset client: %w", err)
	}

	ecsRunner := runner.NewECSRunner(c.ecsClient, c.cfg.ECSCluster, c.logger)

	infra := reconciler.InfraConfig{
		ExecutionRoleARN: c.cfg.RunnerExecutionRoleARN,
		TaskRoleARN:      c.cfg.RunnerTaskRoleARN,
		LogGroup:         c.cfg.RunnerLogGroup,
		Region:           c.ecsClient.Options().Region,
	}

	events := make(chan reconciler.ReconcileEvent, 16)
	rec := reconciler.New(
		c.ssmClient, c.ecsClient,
		c.cfg.SSMParameterName, c.cfg.SSMPollInterval,
		infra, events, c.logger.WithGroup("reconciler"),
	)
	go rec.Run(ctx)

	scaleSets := make(map[string]context.CancelFunc)
	var wg sync.WaitGroup

	for {
		select {
		case <-ctx.Done():
			for _, cancel := range scaleSets {
				cancel()
			}
			wg.Wait()
			return nil
		case event := <-events:
			switch event.Kind {
			case reconciler.EventCreate:
				ssCfg := toScaleSetConfig(event.Config)
				info := &taskdef.TaskDefInfo{
					TaskDefinition: event.TaskDefinition,
					Config:         ssCfg,
				}
				scaleSetName := c.cfg.ScaleSetName(event.Family)
				ssCtx, cancel := context.WithCancel(ctx)
				scaleSets[event.Family] = cancel
				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := c.runScaleSet(ssCtx, scalesetClient, ecsRunner, scaleSetName, event.Family, info); err != nil {
						c.logger.Error("scale set exited with error",
							slog.String("scale_set", scaleSetName),
							slog.String("error", err.Error()),
						)
					}
				}()
			case reconciler.EventUpdate:
				c.logger.Warn("config changed for running scale set, restart required for changes to take effect",
					slog.String("family", event.Family),
					slog.String("event", "config_changed"),
				)
			case reconciler.EventRemove:
				if cancel, ok := scaleSets[event.Family]; ok {
					c.logger.Info("removing scale set",
						slog.String("family", event.Family),
						slog.String("event", "scale_set_removed"),
					)
					cancel()
					delete(scaleSets, event.Family)
				}
			}
		}
	}
}

func (c *Controller) runScaleSet(
	ctx context.Context,
	scalesetClient ScaleSetClient,
	ecsRunner *runner.ECSRunner,
	scaleSetName string,
	taskDefFamily string,
	info *taskdef.TaskDefInfo,
) error {
	logger := c.logger.With(slog.String("scale_set", scaleSetName))
	logger.Info("registering scale set")

	labels := []scaleset.Label{{Name: scaleSetName, Type: "System"}}
	for _, l := range info.Config.ExtraLabels {
		labels = append(labels, scaleset.Label{Name: l, Type: "System"})
	}

	desired := &scaleset.RunnerScaleSet{
		Name:          scaleSetName,
		RunnerGroupID: 1,
		Labels:        labels,
		RunnerSetting: scaleset.RunnerSetting{DisableUpdate: true},
	}
	injectManagedLabel(desired)

	scaleSet, err := scalesetClient.CreateRunnerScaleSet(ctx, desired)
	if err != nil {
		if !strings.Contains(err.Error(), "RunnerScaleSetExistsException") {
			return fmt.Errorf("failed to create runner scale set: %w", err)
		}
		logger.Info("scale set already exists, updating")
		existing, getErr := scalesetClient.GetRunnerScaleSet(ctx, desired.RunnerGroupID, scaleSetName)
		if getErr != nil {
			return fmt.Errorf("failed to get existing scale set: %w", getErr)
		}
		injectManagedLabel(desired)
		scaleSet, err = scalesetClient.UpdateRunnerScaleSet(ctx, existing.ID, desired)
		if err != nil {
			return fmt.Errorf("failed to update existing scale set: %w", err)
		}
	}

	// Scale set registrations are deliberately NOT deleted on shutdown.
	// During ECS deployments, the old task stops before the new one starts.
	// Deleting the registration creates a gap where GitHub sees no scale set,
	// causing queued jobs to fail. The new controller instance picks up the
	// existing registration via the CreateRunnerScaleSet -> "already exists"
	// -> UpdateRunnerScaleSet path.

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "ecs-arc"
	}

	var sessionClient *scaleset.MessageSessionClient
	for attempt := 1; ; attempt++ {
		sessionClient, err = scalesetClient.MessageSessionClient(ctx, scaleSet.ID, hostname)
		if err == nil {
			break
		}
		if !strings.Contains(err.Error(), "SessionConflictException") && !strings.Contains(err.Error(), "409 Conflict") {
			return fmt.Errorf("failed to create message session: %w", err)
		}
		if attempt >= 6 {
			return fmt.Errorf("failed to create message session after %d attempts: %w", attempt, err)
		}
		wait := time.Duration(attempt) * 10 * time.Second
		logger.Warn("session conflict, retrying",
			slog.Int("attempt", attempt),
			slog.Duration("backoff", wait),
		)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	defer func() {
		if err := sessionClient.Close(context.WithoutCancel(ctx)); err != nil {
			logger.Error("failed to close session client", "error", err)
		}
	}()

	l, err := listener.New(sessionClient, listener.Config{
		ScaleSetID: scaleSet.ID,
		MaxRunners: info.Config.MaxRunners,
		Logger:     logger.WithGroup("listener"),
	})
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}

	s := scalerPkg.NewECSScaler(
		scalesetClient,
		ecsRunner,
		scaleSet.ID,
		scaleSetName,
		taskDefFamily,
		info.Config,
		logger.WithGroup("scaler"),
	)

	// Start stale runner reaper
	reaper := runner.NewReaper(
		c.ecsClient, c.cfg.ECSCluster, scaleSetName,
		info.Config.MaxRuntime, 5*time.Minute,
		logger.WithGroup("reaper"),
	)
	go reaper.Run(ctx, 30*time.Second)

	logger.Info("starting listener",
		slog.Int("max_runners", info.Config.MaxRunners),
		slog.Int("min_runners", info.Config.MinRunners),
	)

	return l.Run(ctx, s)
}

func toScaleSetConfig(r *tomlcfg.ResolvedRunnerConfig) taskdef.ScaleSetConfig {
	return taskdef.ScaleSetConfig{
		MaxRunners:       r.MaxRunners,
		MinRunners:       r.MinRunners,
		MaxRuntime:       r.MaxRuntime,
		Subnets:          r.Subnets,
		SecurityGroups:   r.SecurityGroups,
		CapacityProvider: r.CapacityProvider,
		LaunchType:       mapCompatibilityToLaunchType(r.Compatibility),
		ExtraLabels:      r.Labels(),
	}
}

func mapCompatibilityToLaunchType(compat string) ecsTypes.LaunchType {
	switch compat {
	case "FARGATE":
		return ecsTypes.LaunchTypeFargate
	case "EC2":
		return ecsTypes.LaunchTypeEc2
	case "EXTERNAL":
		return ecsTypes.LaunchTypeExternal
	default:
		return ""
	}
}
