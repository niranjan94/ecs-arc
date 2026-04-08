// Package logging provides structured JSON logging for the ecs-arc controller.
package logging

import (
	"log/slog"
	"os"
)

// NewLogger creates a new slog.Logger that outputs structured JSON to stdout.
// The level parameter controls the minimum log level.
func NewLogger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))
}
