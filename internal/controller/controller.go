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
	"github.com/google/go-github/v61/github"
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
	ghClient  *github.Client
	source    reconciler.ConfigSource
	logger    *slog.Logger
}

// New creates a new Controller. source is the TOML config source the reconciler will poll.
func New(cfg *config.Config, ecsClient *ecs.Client, ghClient *github.Client, source reconciler.ConfigSource, logger *slog.Logger) *Controller {
	return &Controller{
		cfg:       cfg,
		ecsClient: ecsClient,
		ghClient:  ghClient,
		source:    source,
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
		c.source, c.ecsClient,
		c.cfg.SSMPollInterval,
		infra, events, c.logger.WithGroup("reconciler"),
	)
	go rec.Run(ctx)

	scaleSets := make(map[string]context.CancelFunc)
	var wg sync.WaitGroup

	handleEvent := func(event reconciler.ReconcileEvent) {
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
			c.deleteScaleSetIfManaged(ctx, scalesetClient, c.cfg.ScaleSetName(event.Family))
		}
	}

	// Startup phase: handle events until the reconciler signals startup is done.
startupLoop:
	for {
		select {
		case <-ctx.Done():
			for _, cancel := range scaleSets {
				cancel()
			}
			wg.Wait()
			return nil
		case <-rec.StartupDone():
			break startupLoop
		case event := <-events:
			handleEvent(event)
		}
	}

	// Drain any events the reconciler emitted before startupDone fired.
drain:
	for {
		select {
		case event := <-events:
			handleEvent(event)
		default:
			break drain
		}
	}

	// Sweep orphan managed scale sets using the desired snapshot at this instant.
	desired := make(map[string]struct{})
	for fam := range rec.DesiredSnapshot() {
		desired[fam] = struct{}{}
	}
	if err := c.cleanupOrphanScaleSets(ctx, scalesetClient, desired); err != nil {
		c.logger.Error("startup sweep error",
			slog.String("error", err.Error()),
		)
	}

	// Start the layer-3 offline-runner reaper as a backstop to layers 1 and 2.
	orr := newOfflineRunnerReaper(
		c.ghClient,
		scalesetClient,
		c.cfg.GitHubOrg,
		func() []string {
			snapshot := rec.DesiredSnapshot()
			names := make([]string, 0, len(snapshot))
			for fam := range snapshot {
				names = append(names, c.cfg.ScaleSetName(fam))
			}
			return names
		},
		c.cfg.OfflineRunnerReaperInterval,
		c.cfg.OfflineRunnerMinAge,
		c.logger.WithGroup("offline-reaper"),
	)
	go orr.Run(ctx)

	// Steady-state loop.
	for {
		select {
		case <-ctx.Done():
			for _, cancel := range scaleSets {
				cancel()
			}
			wg.Wait()
			return nil
		case event := <-events:
			handleEvent(event)
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

	state := runner.NewState()

	s := scalerPkg.NewECSScaler(
		scalesetClient,
		ecsRunner,
		scaleSet.ID,
		scaleSetName,
		taskDefFamily,
		info.Config,
		state,
		logger.WithGroup("scaler"),
	)

	// Start stale runner reaper. Lives across listener reconnects: it
	// shares state with the scaler and is bound to the per-scale-set ctx.
	reaper := runner.NewReaper(
		c.ecsClient,
		scalesetClient,
		c.cfg.ECSCluster, scaleSetName,
		info.Config.MaxRuntime, 5*time.Minute,
		state,
		logger.WithGroup("reaper"),
	)
	go reaper.Run(ctx, 30*time.Second)

	logger.Info("starting listener",
		slog.Int("max_runners", info.Config.MaxRunners),
		slog.Int("min_runners", info.Config.MinRunners),
	)

	// The upstream listener can return mid-poll on transient broker errors
	// (e.g. a 200 OK with an empty body that fails to decode). Wrap session
	// acquisition + listener.Run in a reconnect loop so a single such error
	// does not take this scale set offline until the controller restarts.
	return runListenerWithReconnect(ctx, func(ctx context.Context) error {
		sessionClient, err := acquireMessageSession(ctx, scalesetClient, scaleSet.ID, hostname, logger)
		if err != nil {
			return err
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

		return l.Run(ctx, s)
	}, logger, defaultListenerBackoff)
}

// acquireMessageSession opens a MessageSessionClient, retrying with linear
// backoff on the upstream 409 SessionConflictException. Non-conflict errors
// and ctx cancellation return immediately. The bounded retry budget
// (six attempts) preserves the original startup-path behaviour.
func acquireMessageSession(
	ctx context.Context,
	client ScaleSetClient,
	scaleSetID int,
	hostname string,
	logger *slog.Logger,
) (*scaleset.MessageSessionClient, error) {
	for attempt := 1; ; attempt++ {
		sc, err := client.MessageSessionClient(ctx, scaleSetID, hostname)
		if err == nil {
			return sc, nil
		}
		if !strings.Contains(err.Error(), "SessionConflictException") && !strings.Contains(err.Error(), "409 Conflict") {
			return nil, fmt.Errorf("failed to create message session: %w", err)
		}
		if attempt >= 6 {
			return nil, fmt.Errorf("failed to create message session after %d attempts: %w", attempt, err)
		}
		wait := time.Duration(attempt) * 10 * time.Second
		logger.Warn("session conflict, retrying",
			slog.Int("attempt", attempt),
			slog.Duration("backoff", wait),
		)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

// cleanupOrphanScaleSets deletes any ecs-arc-managed scale set in the
// configured runner group whose name does not correspond to a desired family.
// Failures are logged; the function always returns nil so startup can proceed.
func (c *Controller) cleanupOrphanScaleSets(
	ctx context.Context,
	ssClient ScaleSetClient,
	desiredFamilies map[string]struct{},
) error {
	expectedNames := make(map[string]struct{}, len(desiredFamilies))
	for fam := range desiredFamilies {
		expectedNames[c.cfg.ScaleSetName(fam)] = struct{}{}
	}

	all, err := ssClient.ListRunnerScaleSets(ctx, 1)
	if err != nil {
		c.logger.Error("scale set sweep: list failed",
			slog.String("error", err.Error()),
		)
		return nil
	}

	var listed, skippedUnmanaged, skippedDesired, deleted, failed int
	listed = len(all)
	for _, ss := range all {
		if !hasManagedLabel(ss.Labels) {
			skippedUnmanaged++
			continue
		}
		if _, ok := expectedNames[ss.Name]; ok {
			skippedDesired++
			continue
		}
		if err := ssClient.DeleteRunnerScaleSet(ctx, ss.ID); err != nil {
			failed++
			c.logger.Error("scale set sweep: delete failed",
				slog.String("scale_set", ss.Name),
				slog.Int("scale_set_id", ss.ID),
				slog.String("error", err.Error()),
			)
			continue
		}
		deleted++
		c.logger.Info("scale set sweep: deleted orphan",
			slog.String("scale_set", ss.Name),
			slog.Int("scale_set_id", ss.ID),
			slog.String("event", "scale_set_deleted"),
			slog.String("reason", "orphan_startup"),
		)
	}
	c.logger.Info("scale set sweep complete",
		slog.String("event", "scale_set_sweep_complete"),
		slog.Int("listed", listed),
		slog.Int("skipped_unmanaged", skippedUnmanaged),
		slog.Int("skipped_in_desired", skippedDesired),
		slog.Int("deleted", deleted),
		slog.Int("failed", failed),
	)
	return nil
}

// deleteScaleSetIfManaged looks up a scale set by name and deletes it only if
// it carries the ManagedLabelName marker. Missing scale sets, unmanaged scale
// sets, and transient fetch errors are all no-ops (logged, not returned) so
// EventRemove handling cannot fail.
func (c *Controller) deleteScaleSetIfManaged(
	ctx context.Context,
	ssClient ScaleSetClient,
	name string,
) {
	ss, err := ssClient.GetRunnerScaleSet(ctx, 1, name)
	if err != nil {
		c.logger.Error("lookup for orphan delete failed",
			slog.String("scale_set", name),
			slog.String("error", err.Error()),
		)
		return
	}
	if ss == nil {
		return
	}
	if !hasManagedLabel(ss.Labels) {
		c.logger.Warn("scale set present but unmanaged, leaving alone",
			slog.String("scale_set", name),
		)
		return
	}
	if err := ssClient.DeleteRunnerScaleSet(ctx, ss.ID); err != nil {
		c.logger.Error("delete failed",
			slog.String("scale_set", name),
			slog.Int("scale_set_id", ss.ID),
			slog.String("error", err.Error()),
		)
		return
	}
	c.logger.Info("deleted managed scale set",
		slog.String("scale_set", name),
		slog.Int("scale_set_id", ss.ID),
		slog.String("event", "scale_set_deleted"),
		slog.String("reason", "event_remove"),
	)
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
