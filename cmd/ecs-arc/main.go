// Package main is the entrypoint for the ecs-arc binary.
// It exposes the "controller" subcommand which runs the long-running
// autoscaler service.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ecs-arc",
	Short: "ECS Actions Runner Controller",
	Long: "ecs-arc autoscales GitHub Actions self-hosted runners as ECS tasks.",
	SilenceUsage: true,
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
