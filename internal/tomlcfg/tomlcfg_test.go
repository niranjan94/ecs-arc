package tomlcfg

import (
	"testing"
	"time"
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

func TestResolve_InheritsDefaults(t *testing.T) {
	input := []byte(`
[defaults]
compatibility = "FARGATE"
subnets = ["subnet-abc"]
security_groups = ["sg-123"]
network_mode = "awsvpc"
max_runners = 20
min_runners = 2
max_runtime = "4h"
enable_dind = false

[[runner]]
family = "test-runner"
cpu = 1024
memory = 2048
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	resolved, err := Resolve(cfg)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	r := resolved["test-runner"]
	if r.Compatibility != "FARGATE" {
		t.Errorf("Compatibility = %q, want FARGATE", r.Compatibility)
	}
	if r.MaxRunners != 20 {
		t.Errorf("MaxRunners = %d, want 20", r.MaxRunners)
	}
	if r.MinRunners != 2 {
		t.Errorf("MinRunners = %d, want 2", r.MinRunners)
	}
	if r.MaxRuntime != 4*time.Hour {
		t.Errorf("MaxRuntime = %v, want 4h", r.MaxRuntime)
	}
	if r.EnableDind != false {
		t.Errorf("EnableDind = %v, want false", r.EnableDind)
	}
	if len(r.Subnets) != 1 || r.Subnets[0] != "subnet-abc" {
		t.Errorf("Subnets = %v, want [subnet-abc]", r.Subnets)
	}
}

func TestResolve_RunnerOverridesDefaults(t *testing.T) {
	input := []byte(`
[defaults]
compatibility = "FARGATE"
max_runners = 20

[[runner]]
family = "test-runner"
cpu = 1024
memory = 2048
max_runners = 5
compatibility = "EC2"
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	resolved, err := Resolve(cfg)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	r := resolved["test-runner"]
	if r.MaxRunners != 5 {
		t.Errorf("MaxRunners = %d, want 5 (runner override)", r.MaxRunners)
	}
	if r.Compatibility != "EC2" {
		t.Errorf("Compatibility = %q, want EC2 (runner override)", r.Compatibility)
	}
}

func TestResolve_HardcodedDefaults(t *testing.T) {
	input := []byte(`
[[runner]]
family = "test-runner"
cpu = 1024
memory = 2048
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	resolved, err := Resolve(cfg)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	r := resolved["test-runner"]
	if r.Compatibility != "FARGATE" {
		t.Errorf("Compatibility = %q, want FARGATE (hardcoded default)", r.Compatibility)
	}
	if r.NetworkMode != "awsvpc" {
		t.Errorf("NetworkMode = %q, want awsvpc (hardcoded default)", r.NetworkMode)
	}
	if r.EnableDind != false {
		t.Errorf("EnableDind = %v, want false (hardcoded default)", r.EnableDind)
	}
	if r.MaxRunners != 10 {
		t.Errorf("MaxRunners = %d, want 10 (hardcoded default)", r.MaxRunners)
	}
	if r.MinRunners != 0 {
		t.Errorf("MinRunners = %d, want 0 (hardcoded default)", r.MinRunners)
	}
	if r.MaxRuntime != 6*time.Hour {
		t.Errorf("MaxRuntime = %v, want 6h (hardcoded default)", r.MaxRuntime)
	}
	if r.OS != "LINUX" {
		t.Errorf("OS = %q, want LINUX (hardcoded default)", r.OS)
	}
	if r.RunnerImage != "ghcr.io/actions/actions-runner:latest" {
		t.Errorf("RunnerImage = %q, want default", r.RunnerImage)
	}
	if r.DindImage != "docker:dind" {
		t.Errorf("DindImage = %q, want docker:dind", r.DindImage)
	}
}

func TestResolve_ErrorDuplicateFamily(t *testing.T) {
	input := []byte(`
[[runner]]
family = "dupe"
cpu = 1024
memory = 2048

[[runner]]
family = "dupe"
cpu = 2048
memory = 4096
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, err = Resolve(cfg)
	if err == nil {
		t.Fatal("expected error for duplicate family")
	}
}

func TestResolve_ErrorMissingFamily(t *testing.T) {
	input := []byte(`
[[runner]]
cpu = 1024
memory = 2048
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, err = Resolve(cfg)
	if err == nil {
		t.Fatal("expected error for missing family")
	}
}

func TestResolve_ErrorMissingCPU(t *testing.T) {
	input := []byte(`
[[runner]]
family = "test"
memory = 2048
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, err = Resolve(cfg)
	if err == nil {
		t.Fatal("expected error for missing cpu")
	}
}

func TestResolve_ErrorMissingMemory(t *testing.T) {
	input := []byte(`
[[runner]]
family = "test"
cpu = 1024
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, err = Resolve(cfg)
	if err == nil {
		t.Fatal("expected error for missing memory")
	}
}

func TestResolve_ErrorInvalidArchitecture(t *testing.T) {
	input := []byte(`
[[runner]]
family = "test"
cpu = 1024
memory = 2048
architecture = "SPARC"
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, err = Resolve(cfg)
	if err == nil {
		t.Fatal("expected error for invalid architecture")
	}
}

func TestResolve_FargateDindForcesFalse(t *testing.T) {
	input := []byte(`
[defaults]
compatibility = "FARGATE"
enable_dind = true

[[runner]]
family = "test"
cpu = 1024
memory = 2048
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	resolved, err := Resolve(cfg)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved["test"].EnableDind != false {
		t.Error("EnableDind should be forced false for FARGATE")
	}
}

func TestResolve_ExternalForcesBridgeMode(t *testing.T) {
	input := []byte(`
[defaults]
compatibility = "EXTERNAL"
network_mode = "awsvpc"

[[runner]]
family = "test"
cpu = 1024
memory = 2048
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	resolved, err := Resolve(cfg)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved["test"].NetworkMode != "bridge" {
		t.Errorf("NetworkMode = %q, want bridge (EXTERNAL forces bridge)", resolved["test"].NetworkMode)
	}
}

func TestDeriveAutoLabels_Architecture(t *testing.T) {
	tests := []struct {
		arch string
		want string
	}{
		{"X86_64", "x64"},
		{"ARM64", "arm64"},
	}
	for _, tt := range tests {
		r := &ResolvedRunnerConfig{Architecture: tt.arch, OS: "LINUX", Compatibility: "FARGATE"}
		labels := DeriveAutoLabels(r)
		if !contains(labels, tt.want) {
			t.Errorf("arch %q: labels %v missing %q", tt.arch, labels, tt.want)
		}
	}
}

func TestDeriveAutoLabels_NoArchWhenEmpty(t *testing.T) {
	r := &ResolvedRunnerConfig{Architecture: "", OS: "LINUX", Compatibility: "FARGATE"}
	labels := DeriveAutoLabels(r)
	for _, l := range labels {
		if l == "x64" || l == "arm64" {
			t.Errorf("should not have arch label, got %q", l)
		}
	}
}

func TestDeriveAutoLabels_OS(t *testing.T) {
	r := &ResolvedRunnerConfig{OS: "LINUX", Compatibility: "FARGATE"}
	labels := DeriveAutoLabels(r)
	if !contains(labels, "linux") {
		t.Errorf("labels %v missing linux", labels)
	}
}

func TestDeriveAutoLabels_DinD(t *testing.T) {
	r := &ResolvedRunnerConfig{EnableDind: true, OS: "LINUX", Compatibility: "EC2"}
	labels := DeriveAutoLabels(r)
	if !contains(labels, "docker") {
		t.Errorf("labels %v missing docker", labels)
	}
	if contains(labels, "no-docker") {
		t.Error("should not have no-docker when dind enabled")
	}
}

func TestDeriveAutoLabels_NoDinD(t *testing.T) {
	r := &ResolvedRunnerConfig{EnableDind: false, OS: "LINUX", Compatibility: "FARGATE"}
	labels := DeriveAutoLabels(r)
	if !contains(labels, "no-docker") {
		t.Errorf("labels %v missing no-docker", labels)
	}
	if contains(labels, "docker") {
		t.Error("should not have docker when dind disabled")
	}
}

func TestDeriveAutoLabels_Compatibility(t *testing.T) {
	r := &ResolvedRunnerConfig{OS: "LINUX", Compatibility: "EXTERNAL"}
	labels := DeriveAutoLabels(r)
	if !contains(labels, "external") {
		t.Errorf("labels %v missing external", labels)
	}
}

func TestDeriveAutoLabels_CPU(t *testing.T) {
	tests := []struct {
		cpu  int
		want string
		skip bool
	}{
		{1024, "1cpu", false},
		{2048, "2cpu", false},
		{4096, "4cpu", false},
		{512, "", true},
		{1536, "", true},
	}
	for _, tt := range tests {
		r := &ResolvedRunnerConfig{CPU: tt.cpu, OS: "LINUX", Compatibility: "FARGATE"}
		labels := DeriveAutoLabels(r)
		if tt.skip {
			for _, l := range labels {
				if l == tt.want || (len(l) > 3 && l[len(l)-3:] == "cpu") {
					t.Errorf("cpu %d: should not have cpu label, got %q", tt.cpu, l)
				}
			}
		} else {
			if !contains(labels, tt.want) {
				t.Errorf("cpu %d: labels %v missing %q", tt.cpu, labels, tt.want)
			}
		}
	}
}

func TestDeriveAutoLabels_Memory(t *testing.T) {
	tests := []struct {
		mem  int
		want string
		skip bool
	}{
		{1024, "1gb", false},
		{2048, "2gb", false},
		{8192, "8gb", false},
		{1536, "", true},
	}
	for _, tt := range tests {
		r := &ResolvedRunnerConfig{Memory: tt.mem, OS: "LINUX", Compatibility: "FARGATE"}
		labels := DeriveAutoLabels(r)
		if tt.skip {
			for _, l := range labels {
				if len(l) > 2 && l[len(l)-2:] == "gb" {
					t.Errorf("mem %d: should not have memory label, got %q", tt.mem, l)
				}
			}
		} else {
			if !contains(labels, tt.want) {
				t.Errorf("mem %d: labels %v missing %q", tt.mem, labels, tt.want)
			}
		}
	}
}

func TestLabels_Deduplication(t *testing.T) {
	r := &ResolvedRunnerConfig{
		DimensionLabels: []string{"arm64", "docker"},
		ExtraLabels:     []string{"custom", "arm64"},
		AutoLabels:      []string{"arm64", "docker", "linux"},
	}
	labels := r.Labels()
	counts := make(map[string]int)
	for _, l := range labels {
		counts[l]++
	}
	for label, count := range counts {
		if count > 1 {
			t.Errorf("label %q appears %d times, want 1", label, count)
		}
	}
	// Verify order: dimension labels first, then extra, then auto (deduplicated)
	if len(labels) != 4 { // arm64, docker, custom, linux
		t.Errorf("len(labels) = %d, want 4; labels = %v", len(labels), labels)
	}
}

func TestLabels_Empty(t *testing.T) {
	r := &ResolvedRunnerConfig{}
	labels := r.Labels()
	if len(labels) != 0 {
		t.Errorf("got %v, want empty", labels)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
