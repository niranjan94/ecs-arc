package config

import (
	"testing"
	"time"
)

func TestLoad_AllRequiredSet(t *testing.T) {
	setAllRequired(t)

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
	if cfg.SSMParameterName != "/ecs-arc/runners" {
		t.Errorf("SSMParameterName = %q", cfg.SSMParameterName)
	}
	if cfg.RunnerExecutionRoleARN != "arn:aws:iam::123:role/exec" {
		t.Errorf("RunnerExecutionRoleARN = %q", cfg.RunnerExecutionRoleARN)
	}
	if cfg.RunnerTaskRoleARN != "arn:aws:iam::123:role/task" {
		t.Errorf("RunnerTaskRoleARN = %q", cfg.RunnerTaskRoleARN)
	}
	if cfg.RunnerLogGroup != "/ecs/runners" {
		t.Errorf("RunnerLogGroup = %q", cfg.RunnerLogGroup)
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

func TestLoad_RunnerExtraLabels(t *testing.T) {
	setAllRequired(t)
	t.Setenv("RUNNER_EXTRA_LABELS", "self-hosted, custom-label, gpu")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"self-hosted", "custom-label", "gpu"}
	if len(cfg.RunnerExtraLabels) != len(want) {
		t.Fatalf("got %v, want %v", cfg.RunnerExtraLabels, want)
	}
	for i, l := range cfg.RunnerExtraLabels {
		if l != want[i] {
			t.Errorf("RunnerExtraLabels[%d] = %q, want %q", i, l, want[i])
		}
	}
}

func TestLoad_RunnerExtraLabelsEmpty(t *testing.T) {
	setAllRequired(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RunnerExtraLabels != nil {
		t.Errorf("got %v, want nil", cfg.RunnerExtraLabels)
	}
}

func TestLoad_SSMParameterRequired(t *testing.T) {
	setAllRequired(t)
	t.Setenv("SSM_PARAMETER_NAME", "")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing SSM_PARAMETER_NAME")
	}
}

func TestLoad_RunnerRolesRequired(t *testing.T) {
	setAllRequired(t)
	t.Setenv("RUNNER_EXECUTION_ROLE_ARN", "")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing RUNNER_EXECUTION_ROLE_ARN")
	}
}

func TestLoad_RunnerLogGroupRequired(t *testing.T) {
	setAllRequired(t)
	t.Setenv("RUNNER_LOG_GROUP", "")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing RUNNER_LOG_GROUP")
	}
}

func TestLoad_SSMPollIntervalDefault(t *testing.T) {
	setAllRequired(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SSMPollInterval != 5*time.Minute {
		t.Errorf("SSMPollInterval = %v, want 5m", cfg.SSMPollInterval)
	}
}

func TestLoad_SSMPollIntervalCustom(t *testing.T) {
	setAllRequired(t)
	t.Setenv("SSM_POLL_INTERVAL", "2m")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SSMPollInterval != 2*time.Minute {
		t.Errorf("SSMPollInterval = %v, want 2m", cfg.SSMPollInterval)
	}
}

func TestLoad_RequiresExactlyOneSource(t *testing.T) {
	base := map[string]string{
		"GITHUB_APP_CLIENT_ID":       "id",
		"GITHUB_APP_INSTALLATION_ID": "1",
		"GITHUB_APP_PRIVATE_KEY":     "key",
		"GITHUB_ORG":                 "org",
		"ECS_CLUSTER":                "cluster",
		"RUNNER_EXECUTION_ROLE_ARN":  "arn",
		"RUNNER_TASK_ROLE_ARN":       "arn",
		"RUNNER_LOG_GROUP":           "lg",
	}
	apply := func(t *testing.T, m map[string]string) {
		for k, v := range base {
			t.Setenv(k, v)
		}
		t.Setenv("SSM_PARAMETER_NAME", "")
		t.Setenv("TOML_CONFIG_FILE", "")
		for k, v := range m {
			t.Setenv(k, v)
		}
	}

	t.Run("neither set", func(t *testing.T) {
		apply(t, nil)
		if _, err := Load(); err == nil {
			t.Fatal("expected error when neither source is set")
		}
	})

	t.Run("both set", func(t *testing.T) {
		apply(t, map[string]string{"SSM_PARAMETER_NAME": "a", "TOML_CONFIG_FILE": "/tmp/x.toml"})
		if _, err := Load(); err == nil {
			t.Fatal("expected error when both sources are set")
		}
	})

	t.Run("ssm only", func(t *testing.T) {
		apply(t, map[string]string{"SSM_PARAMETER_NAME": "a"})
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.SSMParameterName != "a" || cfg.TOMLConfigFile != "" {
			t.Fatalf("got SSM=%q File=%q", cfg.SSMParameterName, cfg.TOMLConfigFile)
		}
	})

	t.Run("file only", func(t *testing.T) {
		apply(t, map[string]string{"TOML_CONFIG_FILE": "/tmp/x.toml"})
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.TOMLConfigFile != "/tmp/x.toml" || cfg.SSMParameterName != "" {
			t.Fatalf("got SSM=%q File=%q", cfg.SSMParameterName, cfg.TOMLConfigFile)
		}
	})
}

func setAllRequired(t *testing.T) {
	t.Helper()
	t.Setenv("GITHUB_APP_CLIENT_ID", "Iv1.abc123")
	t.Setenv("GITHUB_APP_INSTALLATION_ID", "67890")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", "-----BEGIN RSA PRIVATE KEY-----\ntest\n-----END RSA PRIVATE KEY-----")
	t.Setenv("GITHUB_ORG", "my-org")
	t.Setenv("ECS_CLUSTER", "my-cluster")
	t.Setenv("SSM_PARAMETER_NAME", "/ecs-arc/runners")
	t.Setenv("RUNNER_EXECUTION_ROLE_ARN", "arn:aws:iam::123:role/exec")
	t.Setenv("RUNNER_TASK_ROLE_ARN", "arn:aws:iam::123:role/task")
	t.Setenv("RUNNER_LOG_GROUP", "/ecs/runners")
}
