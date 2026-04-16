package tomlcfg

import (
	"fmt"
	"strings"
)

// DeriveAutoLabels generates labels from the resolved config fields.
// These labels are automatically applied to every runner and can be used
// in workflow `runs-on` to target specific runner capabilities.
func DeriveAutoLabels(cfg *ResolvedRunnerConfig) []string {
	var labels []string

	// Architecture
	switch cfg.Architecture {
	case "X86_64":
		labels = append(labels, "x64")
	case "ARM64":
		labels = append(labels, "arm64")
	}

	// OS
	if cfg.OS != "" {
		labels = append(labels, strings.ToLower(cfg.OS))
	}

	// Docker-in-Docker
	if cfg.EnableDind {
		labels = append(labels, "docker")
	} else {
		labels = append(labels, "no-docker")
	}

	// Compatibility
	if cfg.Compatibility != "" {
		labels = append(labels, strings.ToLower(cfg.Compatibility))
	}

	// CPU (only exact multiples of 1024)
	if cfg.CPU > 0 && cfg.CPU%1024 == 0 {
		labels = append(labels, fmt.Sprintf("%dcpu", cfg.CPU/1024))
	}

	// Memory (only exact multiples of 1024)
	if cfg.Memory > 0 && cfg.Memory%1024 == 0 {
		labels = append(labels, fmt.Sprintf("%dgb", cfg.Memory/1024))
	}

	return labels
}

// Labels returns the combined, deduplicated labels for this runner.
// Order: dimension labels (from template keys), extra labels (user-specified),
// auto labels (derived from config). Duplicates are removed, keeping first occurrence.
func (r *ResolvedRunnerConfig) Labels() []string {
	seen := make(map[string]struct{})
	var result []string
	for _, sources := range [][]string{r.DimensionLabels, r.ExtraLabels, r.AutoLabels} {
		for _, l := range sources {
			if _, ok := seen[l]; !ok {
				seen[l] = struct{}{}
				result = append(result, l)
			}
		}
	}
	return result
}
