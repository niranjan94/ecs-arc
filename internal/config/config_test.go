package config

import (
	"testing"
)

func TestLoad_AllRequiredSet(t *testing.T) {
	t.Setenv("GITHUB_APP_CLIENT_ID", "Iv1.abc123")
	t.Setenv("GITHUB_APP_INSTALLATION_ID", "67890")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", "-----BEGIN RSA PRIVATE KEY-----\ntest\n-----END RSA PRIVATE KEY-----")
	t.Setenv("GITHUB_ORG", "my-org")
	t.Setenv("ECS_CLUSTER", "my-cluster")
	t.Setenv("ECS_SUBNETS", "subnet-aaa,subnet-bbb")
	t.Setenv("ECS_SECURITY_GROUPS", "sg-xxx")
	t.Setenv("TASK_DEFINITIONS", "runner-small,runner-large")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.GitHubAppClientID != "Iv1.abc123" {
		t.Errorf("got client ID %q, want %q", cfg.GitHubAppClientID, "Iv1.abc123")
	}
	if cfg.GitHubConfigURL != "https://github.com/my-org" {
		t.Errorf("got config URL %q, want %q", cfg.GitHubConfigURL, "https://github.com/my-org")
	}
	if len(cfg.ECSSubnets) != 2 {
		t.Errorf("got %d subnets, want 2", len(cfg.ECSSubnets))
	}
	if len(cfg.TaskDefinitions) != 2 {
		t.Errorf("got %d task defs, want 2", len(cfg.TaskDefinitions))
	}
}

func TestLoad_MissingRequired(t *testing.T) {
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing required env vars")
	}
}

func TestLoad_OptionalPrefix(t *testing.T) {
	setAllRequired(t)
	t.Setenv("SCALESET_NAME_PREFIX", "prod")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ScaleSetNamePrefix != "prod" {
		t.Errorf("got prefix %q, want %q", cfg.ScaleSetNamePrefix, "prod")
	}
}

func TestScaleSetName(t *testing.T) {
	tests := []struct {
		prefix  string
		taskDef string
		want    string
	}{
		{"", "runner-small", "runner-small"},
		{"prod", "runner-small", "prod-runner-small"},
		{"dev", "runner-large", "dev-runner-large"},
	}
	for _, tt := range tests {
		cfg := Config{ScaleSetNamePrefix: tt.prefix}
		got := cfg.ScaleSetName(tt.taskDef)
		if got != tt.want {
			t.Errorf("ScaleSetName(%q) with prefix %q = %q, want %q", tt.taskDef, tt.prefix, got, tt.want)
		}
	}
}

func setAllRequired(t *testing.T) {
	t.Helper()
	t.Setenv("GITHUB_APP_CLIENT_ID", "Iv1.abc123")
	t.Setenv("GITHUB_APP_INSTALLATION_ID", "67890")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", "-----BEGIN RSA PRIVATE KEY-----\ntest\n-----END RSA PRIVATE KEY-----")
	t.Setenv("GITHUB_ORG", "my-org")
	t.Setenv("ECS_CLUSTER", "my-cluster")
	t.Setenv("ECS_SUBNETS", "subnet-aaa")
	t.Setenv("ECS_SECURITY_GROUPS", "sg-xxx")
	t.Setenv("TASK_DEFINITIONS", "runner-small")
}
