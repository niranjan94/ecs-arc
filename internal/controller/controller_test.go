package controller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/actions/scaleset"
	"github.com/niranjan94/ecs-arc/internal/config"
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

func TestCleanupOrphanScaleSets_DeletesUnmatchedManaged(t *testing.T) {
	fake := newFakeScaleSetClient()
	fake.allInGroup = []scaleset.RunnerScaleSet{
		{ID: 1, Name: "runner-small", Labels: []scaleset.Label{{Name: ManagedLabelName, Type: "System"}}},
		{ID: 2, Name: "runner-gone", Labels: []scaleset.Label{{Name: ManagedLabelName, Type: "System"}}},
		{ID: 3, Name: "foreign", Labels: nil},
	}

	desired := map[string]struct{}{"runner-small": {}}
	c := &Controller{cfg: &config.Config{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	if err := c.cleanupOrphanScaleSets(context.Background(), fake, desired); err != nil {
		t.Fatal(err)
	}
	if len(fake.deleteCalls) != 1 || fake.deleteCalls[0] != 2 {
		t.Fatalf("expected DeleteRunnerScaleSet(2), got %+v", fake.deleteCalls)
	}
}

func TestCleanupOrphanScaleSets_ListError_NoDeletes(t *testing.T) {
	fake := newFakeScaleSetClient()
	fake.listErr = errors.New("boom")
	c := &Controller{cfg: &config.Config{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := c.cleanupOrphanScaleSets(context.Background(), fake, map[string]struct{}{}); err != nil {
		t.Fatalf("expected nil (soft fail), got %v", err)
	}
	if len(fake.deleteCalls) != 0 {
		t.Fatalf("expected no deletes, got %+v", fake.deleteCalls)
	}
}

func TestCleanupOrphanScaleSets_HonoursNamePrefix(t *testing.T) {
	fake := newFakeScaleSetClient()
	fake.allInGroup = []scaleset.RunnerScaleSet{
		{ID: 10, Name: "prod-runner-small", Labels: []scaleset.Label{{Name: ManagedLabelName, Type: "System"}}},
		{ID: 11, Name: "prod-runner-old", Labels: []scaleset.Label{{Name: ManagedLabelName, Type: "System"}}},
	}
	desired := map[string]struct{}{"runner-small": {}} // family name
	c := &Controller{
		cfg:    &config.Config{ScaleSetNamePrefix: "prod"},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := c.cleanupOrphanScaleSets(context.Background(), fake, desired); err != nil {
		t.Fatal(err)
	}
	if len(fake.deleteCalls) != 1 || fake.deleteCalls[0] != 11 {
		t.Fatalf("expected DeleteRunnerScaleSet(11), got %+v", fake.deleteCalls)
	}
}

func TestDeleteScaleSetIfManaged_DeletesManaged(t *testing.T) {
	fake := newFakeScaleSetClient()
	fake.byName["runner-gone"] = &scaleset.RunnerScaleSet{
		ID:     42,
		Name:   "runner-gone",
		Labels: []scaleset.Label{{Name: ManagedLabelName, Type: "System"}},
	}
	c := &Controller{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	c.deleteScaleSetIfManaged(context.Background(), fake, "runner-gone")
	if len(fake.deleteCalls) != 1 || fake.deleteCalls[0] != 42 {
		t.Fatalf("expected delete of id=42, got %+v", fake.deleteCalls)
	}
}

func TestDeleteScaleSetIfManaged_SkipsUnmanaged(t *testing.T) {
	fake := newFakeScaleSetClient()
	fake.byName["foreign"] = &scaleset.RunnerScaleSet{ID: 7, Name: "foreign"}
	c := &Controller{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	c.deleteScaleSetIfManaged(context.Background(), fake, "foreign")
	if len(fake.deleteCalls) != 0 {
		t.Fatalf("expected no deletes, got %+v", fake.deleteCalls)
	}
}

func TestDeleteScaleSetIfManaged_MissingIsNoop(t *testing.T) {
	fake := newFakeScaleSetClient()
	c := &Controller{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	c.deleteScaleSetIfManaged(context.Background(), fake, "does-not-exist")
	if len(fake.deleteCalls) != 0 {
		t.Fatalf("expected no deletes, got %+v", fake.deleteCalls)
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

func TestController_EventRemove_DeletesScaleSetOnGitHub(t *testing.T) {
	fake := newFakeScaleSetClient()
	fake.byName["runner-gone"] = &scaleset.RunnerScaleSet{
		ID:     99,
		Name:   "runner-gone",
		Labels: []scaleset.Label{{Name: ManagedLabelName, Type: "System"}},
	}
	c := &Controller{
		cfg:    &config.Config{},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	c.deleteScaleSetIfManaged(context.Background(), fake, "runner-gone")
	if len(fake.deleteCalls) != 1 || fake.deleteCalls[0] != 99 {
		t.Fatalf("expected DeleteRunnerScaleSet(99), got %+v", fake.deleteCalls)
	}
}
