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
	"github.com/niranjan94/ecs-arc/internal/config"
	"github.com/niranjan94/ecs-arc/internal/runner"
	scalerPkg "github.com/niranjan94/ecs-arc/internal/scaler"
	"github.com/niranjan94/ecs-arc/internal/taskdef"
)

// Controller manages the lifecycle of all scale set goroutines.
type Controller struct {
	cfg       *config.Config
	ecsClient *ecs.Client
	logger    *slog.Logger
}

// New creates a new Controller.
func New(cfg *config.Config, ecsClient *ecs.Client, logger *slog.Logger) *Controller {
	return &Controller{
		cfg:       cfg,
		ecsClient: ecsClient,
		logger:    logger,
	}
}

// Run starts all scale set goroutines and blocks until the context is cancelled.
// On cancellation, it cleans up scale set registrations.
func (c *Controller) Run(ctx context.Context) error {
	describer := taskdef.NewECSTaskDefDescriber(c.ecsClient)
	defaults := taskdef.Defaults{
		Subnets:          c.cfg.ECSSubnets,
		SecurityGroups:   c.cfg.ECSSecurityGroups,
		CapacityProvider: c.cfg.ECSCapacityProvider,
		ExtraLabels:      c.cfg.RunnerExtraLabels,
	}

	taskDefs, err := taskdef.LoadAll(ctx, describer, c.cfg.TaskDefinitions, defaults)
	if err != nil {
		return fmt.Errorf("failed to load task definitions: %w", err)
	}

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

	var wg sync.WaitGroup
	errs := make(chan error, len(c.cfg.TaskDefinitions))

	for _, family := range c.cfg.TaskDefinitions {
		info := taskDefs[family]
		scaleSetName := c.cfg.ScaleSetName(family)

		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.runScaleSet(ctx, scalesetClient, ecsRunner, scaleSetName, family, info); err != nil {
				c.logger.Error("scale set exited with error",
					slog.String("scale_set", scaleSetName),
					slog.String("error", err.Error()),
				)
				errs <- fmt.Errorf("scale set %q: %w", scaleSetName, err)
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		return err
	}
	return nil
}

func (c *Controller) runScaleSet(
	ctx context.Context,
	scalesetClient *scaleset.Client,
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
