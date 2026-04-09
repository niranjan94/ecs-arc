package cfn

import (
	"bytes"
	"flag"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var update = flag.Bool("update", false, "update golden file")

func TestRenderGoldenMatch(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, RenderOptions{}); err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	got := buf.Bytes()

	if *update {
		if err := os.WriteFile("testdata/expected.yaml", got, 0644); err != nil {
			t.Fatalf("failed to update golden: %v", err)
		}
		return
	}

	want, err := os.ReadFile("testdata/expected.yaml")
	if err != nil {
		t.Fatalf("failed to read golden: %v", err)
	}

	if !bytes.Equal(got, want) {
		_ = os.WriteFile("testdata/actual.yaml", got, 0644)
		t.Fatalf("rendered output does not match testdata/expected.yaml\n" +
			"wrote actual output to testdata/actual.yaml\n" +
			"diff with: diff -u internal/cfn/testdata/expected.yaml internal/cfn/testdata/actual.yaml\n" +
			"run tests with -update to regenerate the golden file")
	}
}

func TestRenderProducesValidYAML(t *testing.T) {
	out, err := RenderBytes(RenderOptions{})
	if err != nil {
		t.Fatalf("RenderBytes failed: %v", err)
	}
	var parsed any
	if err := yaml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("rendered output is not valid YAML: %v", err)
	}
}

func TestRenderContainsNineRunnerTaskDefs(t *testing.T) {
	out, err := RenderBytes(RenderOptions{})
	if err != nil {
		t.Fatalf("RenderBytes failed: %v", err)
	}
	for _, v := range AllVariants {
		needle := "Runner" + v.Name + "TaskDefinition:"
		if !bytes.Contains(out, []byte(needle)) {
			t.Errorf("rendered output missing logical ID %q", needle)
		}
	}
}

func TestRenderVariantSubset(t *testing.T) {
	subset, err := ParseVariants([]string{"small-x64", "large-arm64"})
	if err != nil {
		t.Fatalf("ParseVariants failed: %v", err)
	}
	out, err := RenderBytes(RenderOptions{Variants: subset})
	if err != nil {
		t.Fatalf("RenderBytes failed: %v", err)
	}
	s := string(out)

	for _, v := range AllVariants {
		needle := "Runner" + v.Name + "TaskDefinition:"
		present := strings.Contains(s, needle)
		want := v.Slug == "small-x64" || v.Slug == "large-arm64"
		if present != want {
			t.Errorf("variant %q: present=%v want=%v", v.Slug, present, want)
		}
	}

	if !strings.Contains(s, "RunnerSmallX64TaskDefinition:\n    Value: !Ref RunnerSmallX64TaskDefinition") {
		t.Error("outputs missing RunnerSmallX64TaskDefinition ref")
	}
}
