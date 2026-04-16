package tomlcfg

import (
	"testing"
)

func TestParse_MinimalExplicitRunner(t *testing.T) {
	input := []byte(`
[defaults]
compatibility = "FARGATE"
subnets = ["subnet-abc"]
network_mode = "awsvpc"

[[runner]]
family = "test-runner"
cpu = 1024
memory = 2048
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if cfg.Defaults.Compatibility != "FARGATE" {
		t.Errorf("Defaults.Compatibility = %q, want %q", cfg.Defaults.Compatibility, "FARGATE")
	}
	if len(cfg.Defaults.Subnets) != 1 || cfg.Defaults.Subnets[0] != "subnet-abc" {
		t.Errorf("Defaults.Subnets = %v, want [subnet-abc]", cfg.Defaults.Subnets)
	}
	if len(cfg.Runners) != 1 {
		t.Fatalf("len(Runners) = %d, want 1", len(cfg.Runners))
	}
	r := cfg.Runners[0]
	if r.Family != "test-runner" {
		t.Errorf("Family = %q, want %q", r.Family, "test-runner")
	}
	if r.CPU != 1024 {
		t.Errorf("CPU = %d, want 1024", r.CPU)
	}
	if r.Memory != 2048 {
		t.Errorf("Memory = %d, want 2048", r.Memory)
	}
}

func TestParse_PointerFieldsNilWhenOmitted(t *testing.T) {
	input := []byte(`
[[runner]]
family = "test"
cpu = 1024
memory = 2048
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	r := cfg.Runners[0]
	if r.EnableDind != nil {
		t.Errorf("EnableDind = %v, want nil (unset)", r.EnableDind)
	}
	if r.MaxRunners != nil {
		t.Errorf("MaxRunners = %v, want nil (unset)", r.MaxRunners)
	}
	if r.RunnerImage != nil {
		t.Errorf("RunnerImage = %v, want nil (unset)", r.RunnerImage)
	}
}

func TestParse_PointerFieldsSetWhenPresent(t *testing.T) {
	input := []byte(`
[[runner]]
family = "test"
cpu = 1024
memory = 2048
enable_dind = true
max_runners = 5
runner_image = "custom:v2"
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	r := cfg.Runners[0]
	if r.EnableDind == nil || *r.EnableDind != true {
		t.Errorf("EnableDind = %v, want ptr(true)", r.EnableDind)
	}
	if r.MaxRunners == nil || *r.MaxRunners != 5 {
		t.Errorf("MaxRunners = %v, want ptr(5)", r.MaxRunners)
	}
	if r.RunnerImage == nil || *r.RunnerImage != "custom:v2" {
		t.Errorf("RunnerImage = %v, want ptr(custom:v2)", r.RunnerImage)
	}
}

func TestParse_DefaultsPointerFieldsNilWhenOmitted(t *testing.T) {
	input := []byte(`
[defaults]
compatibility = "FARGATE"
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if cfg.Defaults.EnableDind != nil {
		t.Errorf("Defaults.EnableDind = %v, want nil", cfg.Defaults.EnableDind)
	}
	if cfg.Defaults.MaxRunners != nil {
		t.Errorf("Defaults.MaxRunners = %v, want nil", cfg.Defaults.MaxRunners)
	}
}

func TestParse_TemplateConfig(t *testing.T) {
	input := []byte(`
[[template]]
family_prefix = "runner"

[template.sizes]
small = { cpu = 1024, memory = 2048 }
large = { cpu = 4096, memory = 8192 }

[template.architectures]
x64 = { architecture = "X86_64" }

[template.features]
plain = {}
docker = { enable_dind = true }

[template.exclude]
combinations = [
    { size = "small", feature = "docker" },
]
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if len(cfg.Templates) != 1 {
		t.Fatalf("len(Templates) = %d, want 1", len(cfg.Templates))
	}
	tmpl := cfg.Templates[0]
	if tmpl.FamilyPrefix != "runner" {
		t.Errorf("FamilyPrefix = %q, want %q", tmpl.FamilyPrefix, "runner")
	}
	if len(tmpl.Sizes) != 2 {
		t.Errorf("len(Sizes) = %d, want 2", len(tmpl.Sizes))
	}
	if len(tmpl.Architectures) != 1 {
		t.Errorf("len(Architectures) = %d, want 1", len(tmpl.Architectures))
	}
	if len(tmpl.Features) != 2 {
		t.Errorf("len(Features) = %d, want 2", len(tmpl.Features))
	}
	if len(tmpl.Exclude.Combinations) != 1 {
		t.Errorf("len(Exclude.Combinations) = %d, want 1", len(tmpl.Exclude.Combinations))
	}
}

func TestParse_InvalidTOML(t *testing.T) {
	input := []byte(`[[[invalid`)
	_, err := Parse(input)
	if err == nil {
		t.Fatal("expected error for invalid TOML")
	}
}
