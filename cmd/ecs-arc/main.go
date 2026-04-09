// Package main is the entrypoint for the ecs-arc multi-command binary.
// It exposes two subcommands: "controller" runs the long-running autoscaler
// service, and "generate-template" renders the CloudFormation template used
// to deploy ecs-arc.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ecs-arc",
	Short: "ECS Actions Runner Controller",
	Long: "ecs-arc autoscales GitHub Actions self-hosted runners as ECS tasks. " +
		"Use the 'controller' subcommand to run the service, or 'generate-template' " +
		"to produce the CloudFormation template used to deploy it.",
	SilenceUsage: true,
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
