// Package main is the entrypoint for the ecs-arc controller.
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/niranjan94/ecs-arc/internal/logging"
)

func main() {
	logger := logging.NewLogger(slog.LevelInfo)
	logger.Info("ecs-arc controller starting")

	if err := run(logger); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	// Will be filled in as we build each component
	logger.Info("ecs-arc controller started (no-op)")
	return nil
}
