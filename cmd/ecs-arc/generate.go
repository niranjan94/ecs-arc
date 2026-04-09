package main

import (
	"fmt"
	"os"

	"github.com/niranjan94/ecs-arc/internal/cfn"
	"github.com/spf13/cobra"
)

var (
	generateOutput   string
	generateVariants []string
)

var generateCmd = &cobra.Command{
	Use:   "generate-template",
	Short: "Generate the CloudFormation template for deploying ecs-arc",
	Long: "Renders the ecs-arc CloudFormation template to stdout or a file. " +
		"Use --variants to emit a subset of the default 9 runner task definitions.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runGenerate()
	},
}

func init() {
	generateCmd.Flags().StringVarP(&generateOutput, "output", "o", "",
		"write template to this file instead of stdout")
	generateCmd.Flags().StringSliceVar(&generateVariants, "variants", nil,
		"comma-separated list of runner variant slugs to include (default: all)")
	rootCmd.AddCommand(generateCmd)
}

func runGenerate() error {
	variants, err := cfn.ParseVariants(generateVariants)
	if err != nil {
		return err
	}
	opts := cfn.RenderOptions{Variants: variants}

	if generateOutput == "" {
		return cfn.Render(os.Stdout, opts)
	}

	// Render to a buffer first so we do not truncate the target file on a
	// template error.
	data, err := cfn.RenderBytes(opts)
	if err != nil {
		return err
	}
	if err := os.WriteFile(generateOutput, data, 0644); err != nil {
		return fmt.Errorf("write %s: %w", generateOutput, err)
	}
	if isTerminal(os.Stderr) {
		fmt.Fprintf(os.Stderr, "wrote %s\n", generateOutput)
	}
	return nil
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
