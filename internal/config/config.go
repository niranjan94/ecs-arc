// Package config handles environment variable parsing and validation for the
// ecs-arc controller. It produces a typed Config struct from environment
// variables, with no AWS or GitHub API calls.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all configuration for the ecs-arc controller.
type Config struct {
	// GitHubAppClientID is the GitHub App Client ID (e.g. "Iv1.abc123").
	GitHubAppClientID string
	// GitHubAppInstallationID is the installation ID of the GitHub App.
	GitHubAppInstallationID int64
	// GitHubAppPrivateKey is the PEM-encoded private key of the GitHub App.
	GitHubAppPrivateKey string
	// GitHubOrg is the GitHub organization name.
	GitHubOrg string
	// GitHubConfigURL is derived from GitHubOrg: "https://github.com/{org}".
	GitHubConfigURL string

	// ScaleSetNamePrefix is an optional prefix for scale set names.
	// When set, scale set names become "{prefix}-{taskDefFamily}".
	ScaleSetNamePrefix string

	// ECSCluster is the ECS cluster name or ARN.
	ECSCluster string

	// SSMParameterName is the SSM Parameter Store parameter that holds the TOML runner config.
	SSMParameterName string
	// TOMLConfigFile is a local filesystem path holding the TOML runner config.
	// Exactly one of TOMLConfigFile or SSMParameterName must be set.
	TOMLConfigFile string
	// SSMPollInterval is how often to poll SSM for config changes.
	SSMPollInterval time.Duration

	// RunnerExecutionRoleARN is the IAM execution role ARN for runner task definitions.
	RunnerExecutionRoleARN string
	// RunnerTaskRoleARN is the IAM task role ARN for runner task definitions.
	RunnerTaskRoleARN string
	// RunnerLogGroup is the CloudWatch log group for runner containers.
	RunnerLogGroup string

	// RunnerExtraLabels are additional GitHub Actions labels to apply to every
	// runner scale set. Comma-separated list of label names.
	RunnerExtraLabels []string

	// GitHubAppID is the numeric GitHub App ID (distinct from the string
	// GitHubAppClientID). Required for the go-github REST client used by
	// the offline runner reaper.
	GitHubAppID int64

	// OfflineRunnerReaperInterval controls how often the controller's backstop
	// reaper sweeps GitHub for offline runner registrations. Default 30m.
	OfflineRunnerReaperInterval time.Duration
	// OfflineRunnerMinAge is the minimum time a runner must be observed
	// offline before the reaper is allowed to deregister it. Default 1h.
	OfflineRunnerMinAge time.Duration
}

// ScaleSetName returns the scale set name for a given task definition family.
// If ScaleSetNamePrefix is set, the name is "{prefix}-{family}".
// Otherwise, the name is just the family name.
func (c *Config) ScaleSetName(taskDefFamily string) string {
	if c.ScaleSetNamePrefix == "" {
		return taskDefFamily
	}
	return c.ScaleSetNamePrefix + "-" + taskDefFamily
}

// Load reads configuration from environment variables and returns a
// validated Config. It returns an error if any required variable is
// missing or invalid.
func Load() (*Config, error) {
	cfg := &Config{}
	var missing []string

	cfg.GitHubAppClientID = os.Getenv("GITHUB_APP_CLIENT_ID")
	if cfg.GitHubAppClientID == "" {
		missing = append(missing, "GITHUB_APP_CLIENT_ID")
	}

	installIDStr := os.Getenv("GITHUB_APP_INSTALLATION_ID")
	if installIDStr == "" {
		missing = append(missing, "GITHUB_APP_INSTALLATION_ID")
	} else {
		id, err := strconv.ParseInt(installIDStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("GITHUB_APP_INSTALLATION_ID must be an integer: %w", err)
		}
		cfg.GitHubAppInstallationID = id
	}

	appIDStr := os.Getenv("GITHUB_APP_ID")
	if appIDStr == "" {
		missing = append(missing, "GITHUB_APP_ID")
	} else {
		id, err := strconv.ParseInt(appIDStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("GITHUB_APP_ID must be an integer: %w", err)
		}
		cfg.GitHubAppID = id
	}

	cfg.GitHubAppPrivateKey = os.Getenv("GITHUB_APP_PRIVATE_KEY")
	if cfg.GitHubAppPrivateKey == "" {
		missing = append(missing, "GITHUB_APP_PRIVATE_KEY")
	}

	cfg.GitHubOrg = os.Getenv("GITHUB_ORG")
	if cfg.GitHubOrg == "" {
		missing = append(missing, "GITHUB_ORG")
	} else {
		cfg.GitHubConfigURL = "https://github.com/" + cfg.GitHubOrg
	}

	cfg.ScaleSetNamePrefix = os.Getenv("SCALESET_NAME_PREFIX")

	cfg.ECSCluster = os.Getenv("ECS_CLUSTER")
	if cfg.ECSCluster == "" {
		missing = append(missing, "ECS_CLUSTER")
	}

	cfg.SSMParameterName = os.Getenv("SSM_PARAMETER_NAME")
	cfg.TOMLConfigFile = os.Getenv("TOML_CONFIG_FILE")
	switch {
	case cfg.SSMParameterName != "" && cfg.TOMLConfigFile != "":
		return nil, fmt.Errorf("set exactly one of SSM_PARAMETER_NAME or TOML_CONFIG_FILE, not both")
	case cfg.SSMParameterName == "" && cfg.TOMLConfigFile == "":
		return nil, fmt.Errorf("one of SSM_PARAMETER_NAME or TOML_CONFIG_FILE must be set")
	}

	if pollStr := os.Getenv("SSM_POLL_INTERVAL"); pollStr != "" {
		d, err := time.ParseDuration(pollStr)
		if err != nil {
			return nil, fmt.Errorf("SSM_POLL_INTERVAL must be a valid duration: %w", err)
		}
		cfg.SSMPollInterval = d
	} else {
		cfg.SSMPollInterval = 5 * time.Minute
	}

	if s := os.Getenv("OFFLINE_RUNNER_REAPER_INTERVAL"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return nil, fmt.Errorf("OFFLINE_RUNNER_REAPER_INTERVAL must be a valid duration: %w", err)
		}
		cfg.OfflineRunnerReaperInterval = d
	} else {
		cfg.OfflineRunnerReaperInterval = 30 * time.Minute
	}

	if s := os.Getenv("OFFLINE_RUNNER_MIN_AGE"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return nil, fmt.Errorf("OFFLINE_RUNNER_MIN_AGE must be a valid duration: %w", err)
		}
		cfg.OfflineRunnerMinAge = d
	} else {
		cfg.OfflineRunnerMinAge = time.Hour
	}

	cfg.RunnerExecutionRoleARN = os.Getenv("RUNNER_EXECUTION_ROLE_ARN")
	if cfg.RunnerExecutionRoleARN == "" {
		missing = append(missing, "RUNNER_EXECUTION_ROLE_ARN")
	}

	cfg.RunnerTaskRoleARN = os.Getenv("RUNNER_TASK_ROLE_ARN")
	if cfg.RunnerTaskRoleARN == "" {
		missing = append(missing, "RUNNER_TASK_ROLE_ARN")
	}

	cfg.RunnerLogGroup = os.Getenv("RUNNER_LOG_GROUP")
	if cfg.RunnerLogGroup == "" {
		missing = append(missing, "RUNNER_LOG_GROUP")
	}

	if extraLabels := os.Getenv("RUNNER_EXTRA_LABELS"); extraLabels != "" {
		cfg.RunnerExtraLabels = splitCSV(extraLabels)
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
