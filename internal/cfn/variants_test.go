package cfn

import (
	"strings"
	"testing"
)

func TestAllVariantsHasNineEntries(t *testing.T) {
	if got, want := len(AllVariants), 9; got != want {
		t.Fatalf("len(AllVariants) = %d, want %d", got, want)
	}
}

func TestAllVariantsSlugs(t *testing.T) {
	expected := []string{
		"small", "medium", "large",
		"small-x64", "medium-x64", "large-x64",
		"small-arm64", "medium-arm64", "large-arm64",
	}
	if len(AllVariants) != len(expected) {
		t.Fatalf("variant count mismatch")
	}
	for i, v := range AllVariants {
		if v.Slug != expected[i] {
			t.Errorf("AllVariants[%d].Slug = %q, want %q", i, v.Slug, expected[i])
		}
	}
}

func TestParseVariantsDefaultsToAll(t *testing.T) {
	got, err := ParseVariants(nil)
	if err != nil {
		t.Fatalf("ParseVariants(nil) error: %v", err)
	}
	if len(got) != len(AllVariants) {
		t.Fatalf("ParseVariants(nil) returned %d variants, want %d", len(got), len(AllVariants))
	}
}

func TestParseVariantsSubset(t *testing.T) {
	got, err := ParseVariants([]string{"small-x64", "large-arm64"})
	if err != nil {
		t.Fatalf("ParseVariants error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Slug != "small-x64" || got[1].Slug != "large-arm64" {
		t.Errorf("unexpected variants: %+v", got)
	}
}

func TestParseVariantsUnknownSlug(t *testing.T) {
	_, err := ParseVariants([]string{"small", "huge"})
	if err == nil {
		t.Fatal("expected error for unknown slug, got nil")
	}
	for _, slug := range []string{"small", "medium", "large", "small-x64"} {
		if !strings.Contains(err.Error(), slug) {
			t.Errorf("error message missing valid slug %q: %v", slug, err)
		}
	}
}
