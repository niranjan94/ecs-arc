// Package config handles environment variable parsing and validation for the
// ecs-arc controller. It produces a typed Config struct from environment
// variables, with no AWS or GitHub API calls.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
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
	// ECSSubnets is the list of subnet IDs for awsvpc network mode.
	ECSSubnets []string
	// ECSSecurityGroups is the list of security group IDs.
	ECSSecurityGroups []string
	// ECSCapacityProvider is the optional capacity provider name.
	ECSCapacityProvider string

	// TaskDefinitions is the list of ECS task definition family names.
	TaskDefinitions []string
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

	if subnets := os.Getenv("ECS_SUBNETS"); subnets != "" {
		cfg.ECSSubnets = splitCSV(subnets)
	}

	if sgs := os.Getenv("ECS_SECURITY_GROUPS"); sgs != "" {
		cfg.ECSSecurityGroups = splitCSV(sgs)
	}

	cfg.ECSCapacityProvider = os.Getenv("ECS_CAPACITY_PROVIDER")

	taskDefs := os.Getenv("TASK_DEFINITIONS")
	if taskDefs == "" {
		missing = append(missing, "TASK_DEFINITIONS")
	} else {
		cfg.TaskDefinitions = splitCSV(taskDefs)
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
