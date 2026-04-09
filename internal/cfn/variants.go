// Package cfn renders the CloudFormation template for ecs-arc deployments.
// It defines the set of runner variants (size x architecture) and provides
// a text/template-based renderer that produces the full CFN YAML.
package cfn

import (
	"fmt"
	"strings"
)

// RunnerVariant describes one runner task definition that the generated
// template should emit. Each variant corresponds to a combination of size
// (Small/Medium/Large, which maps to CFN CPU/Memory parameters) and
// architecture (unspecified, X86_64, or ARM64).
type RunnerVariant struct {
	// Name is the PascalCase suffix used in CloudFormation logical IDs,
	// e.g. "SmallX64" produces "RunnerSmallX64TaskDefinition".
	Name string
	// Slug is the kebab-case suffix used in ECS task definition Family
	// names and in the --variants CLI flag, e.g. "small-x64".
	Slug string
	// SizeName is "Small", "Medium", or "Large". It is used to reference the
	// corresponding CFN parameters (RunnerSmallCpu, RunnerSmallMemory, etc).
	SizeName string
	// Architecture is "", "X86_64", or "ARM64". Empty means the task
	// definition omits the CpuArchitecture field entirely.
	Architecture string
}

// AllVariants is the full set of runner variants emitted by default.
var AllVariants = []RunnerVariant{
	{Name: "Small", Slug: "small", SizeName: "Small", Architecture: ""},
	{Name: "Medium", Slug: "medium", SizeName: "Medium", Architecture: ""},
	{Name: "Large", Slug: "large", SizeName: "Large", Architecture: ""},
	{Name: "SmallX64", Slug: "small-x64", SizeName: "Small", Architecture: "X86_64"},
	{Name: "MediumX64", Slug: "medium-x64", SizeName: "Medium", Architecture: "X86_64"},
	{Name: "LargeX64", Slug: "large-x64", SizeName: "Large", Architecture: "X86_64"},
	{Name: "SmallArm64", Slug: "small-arm64", SizeName: "Small", Architecture: "ARM64"},
	{Name: "MediumArm64", Slug: "medium-arm64", SizeName: "Medium", Architecture: "ARM64"},
	{Name: "LargeArm64", Slug: "large-arm64", SizeName: "Large", Architecture: "ARM64"},
}

// ParseVariants resolves a list of slugs to the matching RunnerVariant
// entries, preserving the caller's order. A nil or empty input returns
// AllVariants. Unknown slugs produce an error that lists all valid slugs
// so the user can correct their input.
func ParseVariants(slugs []string) ([]RunnerVariant, error) {
	if len(slugs) == 0 {
		return AllVariants, nil
	}
	bySlug := make(map[string]RunnerVariant, len(AllVariants))
	valid := make([]string, 0, len(AllVariants))
	for _, v := range AllVariants {
		bySlug[v.Slug] = v
		valid = append(valid, v.Slug)
	}
	out := make([]RunnerVariant, 0, len(slugs))
	for _, s := range slugs {
		v, ok := bySlug[s]
		if !ok {
			return nil, fmt.Errorf("unknown variant %q; valid variants: %s", s, strings.Join(valid, ", "))
		}
		out = append(out, v)
	}
	return out, nil
}
