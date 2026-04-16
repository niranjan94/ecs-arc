package tomlcfg

import (
	"fmt"
	"sort"
)

// dimension is one axis of the template cross product.
type dimension struct {
	name    string // "size", "architecture", or "feature"
	keys    []string
	entries map[string]DimensionEntry
}

// ExpandTemplates expands [[template]] entries into RunnerConfig entries via
// cross product of dimensions. Returns nil if no templates are defined.
func ExpandTemplates(cfg *Config) ([]RunnerConfig, error) {
	if len(cfg.Templates) == 0 {
		return nil, nil
	}

	var all []RunnerConfig
	for i, tmpl := range cfg.Templates {
		expanded, err := expandOneTemplate(tmpl, i)
		if err != nil {
			return nil, err
		}
		all = append(all, expanded...)
	}
	return all, nil
}

func expandOneTemplate(tmpl TemplateConfig, index int) ([]RunnerConfig, error) {
	if tmpl.FamilyPrefix == "" {
		return nil, fmt.Errorf("template[%d]: missing required field: family_prefix", index)
	}

	// Collect present dimensions in fixed order: sizes, architectures, features
	var dims []dimension
	if len(tmpl.Sizes) > 0 {
		dims = append(dims, dimension{name: "size", keys: sortedKeys(tmpl.Sizes), entries: tmpl.Sizes})
	}
	if len(tmpl.Architectures) > 0 {
		dims = append(dims, dimension{name: "architecture", keys: sortedKeys(tmpl.Architectures), entries: tmpl.Architectures})
	}
	if len(tmpl.Features) > 0 {
		dims = append(dims, dimension{name: "feature", keys: sortedKeys(tmpl.Features), entries: tmpl.Features})
	}

	if len(dims) == 0 {
		return nil, fmt.Errorf("template[%d] %q: must have at least one dimension (sizes, architectures, or features)", index, tmpl.FamilyPrefix)
	}

	// Generate cross product
	combos := crossProduct(dims)

	// Filter out excluded combinations
	var runners []RunnerConfig
	for _, combo := range combos {
		if isExcluded(combo, tmpl.Exclude.Combinations) {
			continue
		}

		// Build family name: prefix-key1-key2-...
		family := tmpl.FamilyPrefix
		var dimLabels []string
		for _, c := range combo {
			family += "-" + c.key
			dimLabels = append(dimLabels, c.key)
		}

		// Merge dimension entries in order (later overrides earlier):
		// sizes -> architectures -> features
		r := RunnerConfig{
			Family:          family,
			DimensionLabels: dimLabels,
		}
		for _, c := range combo {
			mergeDimensionEntry(&r, c.entry)
		}

		runners = append(runners, r)
	}

	return runners, nil
}

// comboEntry is one dimension's key + entry in a combination.
type comboEntry struct {
	dimName string
	key     string
	entry   DimensionEntry
}

// crossProduct generates all combinations across dimensions.
func crossProduct(dims []dimension) [][]comboEntry {
	if len(dims) == 0 {
		return [][]comboEntry{{}}
	}

	first := dims[0]
	rest := crossProduct(dims[1:])

	var result [][]comboEntry
	for _, key := range first.keys {
		for _, suffix := range rest {
			combo := make([]comboEntry, 0, len(dims))
			combo = append(combo, comboEntry{dimName: first.name, key: key, entry: first.entries[key]})
			combo = append(combo, suffix...)
			result = append(result, combo)
		}
	}
	return result
}

// isExcluded checks if a combination matches any exclude entry.
// An exclude entry with empty fields acts as a wildcard for those dimensions.
func isExcluded(combo []comboEntry, excludes []ExcludeEntry) bool {
	for _, ex := range excludes {
		if matchesExclude(combo, ex) {
			return true
		}
	}
	return false
}

func matchesExclude(combo []comboEntry, ex ExcludeEntry) bool {
	for _, c := range combo {
		switch c.dimName {
		case "size":
			if ex.Size != "" && ex.Size != c.key {
				return false
			}
		case "architecture":
			if ex.Architecture != "" && ex.Architecture != c.key {
				return false
			}
		case "feature":
			if ex.Feature != "" && ex.Feature != c.key {
				return false
			}
		}
	}
	return true
}

// mergeDimensionEntry applies non-zero fields from a DimensionEntry onto a RunnerConfig.
func mergeDimensionEntry(r *RunnerConfig, d DimensionEntry) {
	if d.CPU != 0 {
		r.CPU = d.CPU
	}
	if d.Memory != 0 {
		r.Memory = d.Memory
	}
	if d.Architecture != "" {
		r.Architecture = d.Architecture
	}
	if d.OS != "" {
		r.OS = d.OS
	}
	if d.EnableDind != nil {
		r.EnableDind = d.EnableDind
	}
	if d.MaxRunners != nil {
		r.MaxRunners = d.MaxRunners
	}
	if d.MinRunners != nil {
		r.MinRunners = d.MinRunners
	}
	if d.Compatibility != nil {
		r.Compatibility = d.Compatibility
	}
	if d.NetworkMode != nil {
		r.NetworkMode = d.NetworkMode
	}
	if d.RunnerImage != nil {
		r.RunnerImage = d.RunnerImage
	}
	if d.DindImage != nil {
		r.DindImage = d.DindImage
	}
	if d.MaxRuntime != nil {
		r.MaxRuntime = d.MaxRuntime
	}
	if d.CapacityProvider != nil {
		r.CapacityProvider = d.CapacityProvider
	}
	if len(d.ExtraLabels) > 0 {
		r.ExtraLabels = append(r.ExtraLabels, d.ExtraLabels...)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
