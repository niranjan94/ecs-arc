// Package controller is the top-level orchestrator for the ecs-arc controller.
// It wires together the scaleset client, listener, scaler, and runner for
// each configured scale set, managing their lifecycles as goroutines.
package controller

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"

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

	scaleSet, err := scalesetClient.CreateRunnerScaleSet(ctx, &scaleset.RunnerScaleSet{
		Name:          scaleSetName,
		RunnerGroupID: 1,
		Labels:        []scaleset.Label{{Name: scaleSetName, Type: "System"}},
		RunnerSetting: scaleset.RunnerSetting{DisableUpdate: true},
	})
	if err != nil {
		return fmt.Errorf("failed to create runner scale set: %w", err)
	}

	defer func() {
		logger.Info("deleting scale set registration")
		if err := scalesetClient.DeleteRunnerScaleSet(context.WithoutCancel(ctx), scaleSet.ID); err != nil {
			logger.Error("failed to delete scale set", slog.String("error", err.Error()))
		}
	}()

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "ecs-arc"
	}

	sessionClient, err := scalesetClient.MessageSessionClient(ctx, scaleSet.ID, hostname)
	if err != nil {
		return fmt.Errorf("failed to create message session: %w", err)
	}
	defer sessionClient.Close(context.WithoutCancel(ctx))

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

	logger.Info("starting listener",
		slog.Int("max_runners", info.Config.MaxRunners),
		slog.Int("min_runners", info.Config.MinRunners),
	)

	return l.Run(ctx, s)
}
