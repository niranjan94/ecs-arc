// Package main is the entrypoint for the ecs-arc controller.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/niranjan94/ecs-arc/internal/config"
	"github.com/niranjan94/ecs-arc/internal/controller"
	"github.com/niranjan94/ecs-arc/internal/logging"
)

func main() {
	logger := logging.NewLogger(slog.LevelInfo)

	if err := run(logger); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	logger.Info("ecs-arc controller starting",
		slog.String("org", cfg.GitHubOrg),
		slog.String("cluster", cfg.ECSCluster),
		slog.Int("scale_sets", len(cfg.TaskDefinitions)),
	)

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}

	ecsClient := ecs.NewFromConfig(awsCfg)
	ctrl := controller.New(cfg, ecsClient, logger)

	return ctrl.Run(ctx)
}
