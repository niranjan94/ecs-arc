// Package main implements the controller subcommand.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/niranjan94/ecs-arc/internal/config"
	"github.com/niranjan94/ecs-arc/internal/controller"
	"github.com/niranjan94/ecs-arc/internal/logging"
	"github.com/niranjan94/ecs-arc/internal/reconciler"
	"github.com/spf13/cobra"
)

var controllerCmd = &cobra.Command{
	Use:   "controller",
	Short: "Run the ecs-arc controller service",
	Long: "Starts the long-running ecs-arc controller, which registers runner " +
		"scale sets with GitHub, listens for job assignment messages, and scales " +
		"ECS tasks up and down to match demand.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runController()
	},
}

func init() {
	rootCmd.AddCommand(controllerCmd)
}

func runController() error {
	logger := logging.NewLogger(slog.LevelInfo)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	logger.Info("ecs-arc controller starting",
		slog.String("org", cfg.GitHubOrg),
		slog.String("cluster", cfg.ECSCluster),
	)

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}

	ecsClient := ecs.NewFromConfig(awsCfg)

	var source reconciler.ConfigSource
	switch {
	case cfg.TOMLConfigFile != "":
		source = reconciler.NewFileSource(cfg.TOMLConfigFile)
		logger.Info("reading runner config from file", slog.String("path", cfg.TOMLConfigFile))
	default:
		ssmClient := ssm.NewFromConfig(awsCfg)
		source = reconciler.NewSSMSource(ssmClient, cfg.SSMParameterName)
		logger.Info("reading runner config from SSM", slog.String("parameter", cfg.SSMParameterName))
	}

	ctrl := controller.New(cfg, ecsClient, source, logger)

	return ctrl.Run(ctx)
}
