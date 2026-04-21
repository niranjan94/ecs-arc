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
	if r.RunnerImage != "ghcr.io/niranjan94/ecs-arc-runner:latest" {
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

func TestExpandTemplates_AllThreeDimensions(t *testing.T) {
	input := []byte(`
[[template]]
family_prefix = "runner"

[template.sizes]
small = { cpu = 1024, memory = 2048 }
large = { cpu = 4096, memory = 8192 }

[template.architectures]
x64 = { architecture = "X86_64" }
arm64 = { architecture = "ARM64" }

[template.features]
plain = {}
docker = { enable_dind = true }
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	expanded, err := ExpandTemplates(cfg)
	if err != nil {
		t.Fatalf("ExpandTemplates: %v", err)
	}
	// 2 sizes x 2 archs x 2 features = 8
	if len(expanded) != 8 {
		t.Fatalf("len(expanded) = %d, want 8", len(expanded))
	}

	families := make(map[string]bool)
	for _, r := range expanded {
		families[r.Family] = true
	}
	expected := []string{
		"runner-large-arm64-docker",
		"runner-large-arm64-plain",
		"runner-large-x64-docker",
		"runner-large-x64-plain",
		"runner-small-arm64-docker",
		"runner-small-arm64-plain",
		"runner-small-x64-docker",
		"runner-small-x64-plain",
	}
	for _, f := range expected {
		if !families[f] {
			t.Errorf("missing family %q", f)
		}
	}
}

func TestExpandTemplates_WithExclude(t *testing.T) {
	input := []byte(`
[[template]]
family_prefix = "runner"

[template.sizes]
small = { cpu = 1024, memory = 2048 }
large = { cpu = 4096, memory = 8192 }

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
		t.Fatalf("Parse: %v", err)
	}
	expanded, err := ExpandTemplates(cfg)
	if err != nil {
		t.Fatalf("ExpandTemplates: %v", err)
	}
	// 2 sizes x 2 features - 1 exclude = 3
	if len(expanded) != 3 {
		t.Fatalf("len(expanded) = %d, want 3", len(expanded))
	}
	for _, r := range expanded {
		if r.Family == "runner-small-docker" {
			t.Error("excluded combination runner-small-docker should not be present")
		}
	}
}

func TestExpandTemplates_SizesOnly(t *testing.T) {
	input := []byte(`
[[template]]
family_prefix = "runner"

[template.sizes]
small = { cpu = 1024, memory = 2048 }
large = { cpu = 4096, memory = 8192 }
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	expanded, err := ExpandTemplates(cfg)
	if err != nil {
		t.Fatalf("ExpandTemplates: %v", err)
	}
	if len(expanded) != 2 {
		t.Fatalf("len(expanded) = %d, want 2", len(expanded))
	}
	families := make(map[string]bool)
	for _, r := range expanded {
		families[r.Family] = true
	}
	if !families["runner-large"] {
		t.Error("missing family runner-large")
	}
	if !families["runner-small"] {
		t.Error("missing family runner-small")
	}
}

func TestExpandTemplates_TwoDimensions(t *testing.T) {
	input := []byte(`
[[template]]
family_prefix = "runner"

[template.sizes]
small = { cpu = 1024, memory = 2048 }

[template.features]
plain = {}
docker = { enable_dind = true }
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	expanded, err := ExpandTemplates(cfg)
	if err != nil {
		t.Fatalf("ExpandTemplates: %v", err)
	}
	// 1 size x 2 features = 2
	if len(expanded) != 2 {
		t.Fatalf("len(expanded) = %d, want 2", len(expanded))
	}
	families := make(map[string]bool)
	for _, r := range expanded {
		families[r.Family] = true
	}
	if !families["runner-small-docker"] {
		t.Error("missing runner-small-docker")
	}
	if !families["runner-small-plain"] {
		t.Error("missing runner-small-plain")
	}
}

func TestExpandTemplates_DimensionMergeOrder(t *testing.T) {
	// features override architectures override sizes
	input := []byte(`
[[template]]
family_prefix = "runner"

[template.sizes]
small = { cpu = 1024, memory = 2048, max_runners = 10 }

[template.features]
custom = { max_runners = 5 }
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	expanded, err := ExpandTemplates(cfg)
	if err != nil {
		t.Fatalf("ExpandTemplates: %v", err)
	}
	if len(expanded) != 1 {
		t.Fatalf("len(expanded) = %d, want 1", len(expanded))
	}
	r := expanded[0]
	if r.MaxRunners == nil || *r.MaxRunners != 5 {
		t.Errorf("MaxRunners = %v, want ptr(5) (feature overrides size)", r.MaxRunners)
	}
}

func TestExpandTemplates_DimensionLabels(t *testing.T) {
	input := []byte(`
[[template]]
family_prefix = "runner"

[template.sizes]
small = { cpu = 1024, memory = 2048 }

[template.architectures]
arm64 = { architecture = "ARM64" }

[template.features]
docker = { enable_dind = true }
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	expanded, err := ExpandTemplates(cfg)
	if err != nil {
		t.Fatalf("ExpandTemplates: %v", err)
	}
	if len(expanded) != 1 {
		t.Fatalf("len(expanded) = %d, want 1", len(expanded))
	}
	r := expanded[0]
	want := []string{"small", "arm64", "docker"}
	if len(r.DimensionLabels) != len(want) {
		t.Fatalf("DimensionLabels = %v, want %v", r.DimensionLabels, want)
	}
	for i, l := range r.DimensionLabels {
		if l != want[i] {
			t.Errorf("DimensionLabels[%d] = %q, want %q", i, l, want[i])
		}
	}
}

func TestExpandTemplates_ErrorNoFamilyPrefix(t *testing.T) {
	input := []byte(`
[[template]]
[template.sizes]
small = { cpu = 1024, memory = 2048 }
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, err = ExpandTemplates(cfg)
	if err == nil {
		t.Fatal("expected error for missing family_prefix")
	}
}

func TestExpandTemplates_ErrorNoDimensions(t *testing.T) {
	input := []byte(`
[[template]]
family_prefix = "runner"
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, err = ExpandTemplates(cfg)
	if err == nil {
		t.Fatal("expected error for no dimensions")
	}
}

func TestExpandTemplates_NoTemplates(t *testing.T) {
	input := []byte(`
[[runner]]
family = "test"
cpu = 1024
memory = 2048
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	expanded, err := ExpandTemplates(cfg)
	if err != nil {
		t.Fatalf("ExpandTemplates: %v", err)
	}
	if len(expanded) != 0 {
		t.Errorf("len(expanded) = %d, want 0", len(expanded))
	}
}

func TestExpandTemplates_ExcludePartialMatch(t *testing.T) {
	// Exclude with only size specified should match all combos with that size
	input := []byte(`
[[template]]
family_prefix = "runner"

[template.sizes]
small = { cpu = 1024, memory = 2048 }
large = { cpu = 4096, memory = 8192 }

[template.features]
plain = {}
docker = { enable_dind = true }

[template.exclude]
combinations = [
    { size = "small" },
]
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	expanded, err := ExpandTemplates(cfg)
	if err != nil {
		t.Fatalf("ExpandTemplates: %v", err)
	}
	// small-plain and small-docker both excluded, leaving large-docker and large-plain
	if len(expanded) != 2 {
		t.Fatalf("len(expanded) = %d, want 2", len(expanded))
	}
	for _, r := range expanded {
		if r.Family == "runner-small-plain" || r.Family == "runner-small-docker" {
			t.Errorf("excluded combination %q should not be present", r.Family)
		}
	}
}

func TestResolve_FullPipelineWithTemplateAndExplicit(t *testing.T) {
	input := []byte(`
[defaults]
compatibility = "EXTERNAL"
network_mode = "bridge"
enable_dind = false
max_runtime = "6h"

[[template]]
family_prefix = "runner"

[template.sizes]
small = { cpu = 1024, memory = 2048, max_runners = 24 }
large = { cpu = 4096, memory = 8192, max_runners = 6 }

[template.features]
plain = {}
docker = { enable_dind = true }

[template.exclude]
combinations = [
    { size = "small", feature = "docker" },
]

[[runner]]
family = "custom-runner"
cpu = 8192
memory = 16384
enable_dind = true
compatibility = "EC2"
network_mode = "awsvpc"
subnets = ["subnet-abc"]
security_groups = ["sg-123"]
extra_labels = ["gpu"]
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	resolved, err := Resolve(cfg)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// 3 from template (small-plain, large-docker, large-plain) + 1 explicit = 4
	if len(resolved) != 4 {
		t.Fatalf("len(resolved) = %d, want 4", len(resolved))
	}

	// Check template-expanded runner
	sp := resolved["runner-small-plain"]
	if sp == nil {
		t.Fatal("missing runner-small-plain")
	}
	if sp.CPU != 1024 {
		t.Errorf("runner-small-plain CPU = %d, want 1024", sp.CPU)
	}
	if sp.EnableDind != false {
		t.Error("runner-small-plain EnableDind should be false")
	}
	if sp.MaxRunners != 24 {
		t.Errorf("runner-small-plain MaxRunners = %d, want 24", sp.MaxRunners)
	}
	if sp.Compatibility != "EXTERNAL" {
		t.Errorf("runner-small-plain Compatibility = %q, want EXTERNAL", sp.Compatibility)
	}
	if sp.NetworkMode != "bridge" {
		t.Errorf("runner-small-plain NetworkMode = %q, want bridge (EXTERNAL forces bridge)", sp.NetworkMode)
	}

	// Check labels on template runner
	labels := sp.Labels()
	if !contains(labels, "small") {
		t.Errorf("runner-small-plain labels %v missing dimension label 'small'", labels)
	}
	if !contains(labels, "plain") {
		t.Errorf("runner-small-plain labels %v missing dimension label 'plain'", labels)
	}
	if !contains(labels, "no-docker") {
		t.Errorf("runner-small-plain labels %v missing auto label 'no-docker'", labels)
	}

	// Check explicit runner
	cr := resolved["custom-runner"]
	if cr == nil {
		t.Fatal("missing custom-runner")
	}
	if cr.CPU != 8192 {
		t.Errorf("custom-runner CPU = %d, want 8192", cr.CPU)
	}
	if cr.EnableDind != true {
		t.Error("custom-runner EnableDind should be true")
	}
	if cr.Compatibility != "EC2" {
		t.Errorf("custom-runner Compatibility = %q, want EC2", cr.Compatibility)
	}
	if !contains(cr.Labels(), "gpu") {
		t.Errorf("custom-runner labels %v missing 'gpu'", cr.Labels())
	}
}

func TestResolve_DefaultsExtraLabels(t *testing.T) {
	input := []byte(`
[defaults]
extra_labels = ["self-hosted", "team-x"]

[[runner]]
family = "explicit"
cpu = 1024
memory = 2048
extra_labels = ["gpu"]

[[template]]
family_prefix = "tpl"

[template.sizes]
small = { cpu = 1024, memory = 2048 }

[template.features]
plain = {}
`)
	cfg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	resolved, err := Resolve(cfg)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	exp := resolved["explicit"]
	if exp == nil {
		t.Fatal("missing explicit runner")
	}
	expLabels := exp.Labels()
	for _, want := range []string{"self-hosted", "team-x", "gpu"} {
		if !contains(expLabels, want) {
			t.Errorf("explicit labels %v missing %q", expLabels, want)
		}
	}

	tpl := resolved["tpl-small-plain"]
	if tpl == nil {
		t.Fatal("missing tpl-small-plain runner")
	}
	tplLabels := tpl.Labels()
	for _, want := range []string{"self-hosted", "team-x", "small", "plain"} {
		if !contains(tplLabels, want) {
			t.Errorf("tpl-small-plain labels %v missing %q", tplLabels, want)
		}
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
