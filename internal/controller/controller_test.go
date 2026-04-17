package controller

import (
	"testing"

	"github.com/actions/scaleset"
)

func TestInjectManagedLabel_AddsLabelToDesired(t *testing.T) {
	desired := &scaleset.RunnerScaleSet{
		Name:   "runner-small",
		Labels: []scaleset.Label{{Name: "runner-small", Type: "System"}},
	}
	injectManagedLabel(desired)

	var found bool
	for _, l := range desired.Labels {
		if l.Name == ManagedLabelName && l.Type == "System" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %q label, got %+v", ManagedLabelName, desired.Labels)
	}
}

func TestInjectManagedLabel_Idempotent(t *testing.T) {
	desired := &scaleset.RunnerScaleSet{
		Labels: []scaleset.Label{
			{Name: ManagedLabelName, Type: "System"},
		},
	}
	injectManagedLabel(desired)
	injectManagedLabel(desired)

	count := 0
	for _, l := range desired.Labels {
		if l.Name == ManagedLabelName {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one %q label, found %d (labels=%+v)", ManagedLabelName, count, desired.Labels)
	}
}

func TestRunScaleSet_CreatePath_InjectsManagedLabel(t *testing.T) {
	fake := newFakeScaleSetClient()
	desired := &scaleset.RunnerScaleSet{Name: "x", RunnerGroupID: 1}
	injectManagedLabel(desired)
	if _, err := fake.CreateRunnerScaleSet(nil, desired); err != nil {
		t.Fatal(err)
	}
	if !hasManagedLabel(fake.createCalls[0].Labels) {
		t.Fatalf("create did not include managed label: %+v", fake.createCalls[0].Labels)
	}
}

func TestHasManagedLabel(t *testing.T) {
	cases := []struct {
		name   string
		labels []scaleset.Label
		want   bool
	}{
		{"empty", nil, false},
		{"unrelated", []scaleset.Label{{Name: "other"}}, false},
		{"present", []scaleset.Label{{Name: ManagedLabelName, Type: "System"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasManagedLabel(tc.labels); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
