package controller

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/go-github/v61/github"
)

func ptrInt64(v int64) *int64    { return &v }
func ptrString(v string) *string { return &v }
func ptrBool(v bool) *bool       { return &v }

func newRunner(id int64, name, status string, busy bool) *github.Runner {
	return &github.Runner{
		ID:     ptrInt64(id),
		Name:   ptrString(name),
		Status: ptrString(status),
		Busy:   ptrBool(busy),
	}
}

func TestShouldDelete_OfflineMatchingOldEnough(t *testing.T) {
	r := newRunner(1, "our-set-deadbeef", "offline", false)
	firstSeen := time.Now().Add(-2 * time.Hour)
	now := time.Now()
	if !shouldDelete(r, []string{"our-set"}, firstSeen, now, time.Hour) {
		t.Fatal("eligible runner should be deletable")
	}
}

func TestShouldDelete_Online_Skip(t *testing.T) {
	r := newRunner(1, "our-set-deadbeef", "online", false)
	firstSeen := time.Now().Add(-2 * time.Hour)
	if shouldDelete(r, []string{"our-set"}, firstSeen, time.Now(), time.Hour) {
		t.Fatal("online runner should be skipped")
	}
}

func TestShouldDelete_NoPatternMatch_Skip(t *testing.T) {
	r := newRunner(1, "foreign-runner-deadbeef", "offline", false)
	firstSeen := time.Now().Add(-2 * time.Hour)
	if shouldDelete(r, []string{"our-set"}, firstSeen, time.Now(), time.Hour) {
		t.Fatal("runner whose prefix does not match any desired scale set should be skipped")
	}
}

func TestShouldDelete_WrongSuffix_Skip(t *testing.T) {
	r := newRunner(1, "our-set-notahex", "offline", false)
	firstSeen := time.Now().Add(-2 * time.Hour)
	if shouldDelete(r, []string{"our-set"}, firstSeen, time.Now(), time.Hour) {
		t.Fatal("runner with non-hex suffix must be skipped")
	}
}

func TestShouldDelete_Busy_Skip(t *testing.T) {
	r := newRunner(1, "our-set-deadbeef", "offline", true)
	firstSeen := time.Now().Add(-2 * time.Hour)
	if shouldDelete(r, []string{"our-set"}, firstSeen, time.Now(), time.Hour) {
		t.Fatal("busy runner must not be deleted even if offline")
	}
}

func TestShouldDelete_TooYoung_Skip(t *testing.T) {
	r := newRunner(1, "our-set-deadbeef", "offline", false)
	firstSeen := time.Now().Add(-1 * time.Minute)
	if shouldDelete(r, []string{"our-set"}, firstSeen, time.Now(), time.Hour) {
		t.Fatal("runner younger than minAge must not be deleted")
	}
}

func TestShouldDelete_PrefixMatchesTwoSets_PicksEither(t *testing.T) {
	r := newRunner(1, "our-set-large-deadbeef", "offline", false)
	firstSeen := time.Now().Add(-2 * time.Hour)
	if !shouldDelete(r, []string{"our-set", "our-set-large"}, firstSeen, time.Now(), time.Hour) {
		t.Fatal("runner matching the more specific scale set must be eligible")
	}
}

// newFakeGitHubServer returns an httptest server serving a fixed runner
// list on /orgs/:org/actions/runners.
func newFakeGitHubServer(t *testing.T, runners []*github.Runner) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/orgs/test-org/actions/runners", func(w http.ResponseWriter, r *http.Request) {
		resp := struct {
			TotalCount int              `json:"total_count"`
			Runners    []*github.Runner `json:"runners"`
		}{TotalCount: len(runners), Runners: runners}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	return httptest.NewServer(mux)
}

type testClock struct {
	now time.Time
}

func (c *testClock) Now() time.Time { return c.now }

func TestOfflineReaper_Sweep_PromotesAcrossTwoTicks(t *testing.T) {
	runners := []*github.Runner{
		newRunner(100, "our-set-deadbeef", "offline", false),
	}
	srv := newFakeGitHubServer(t, runners)
	t.Cleanup(srv.Close)

	gh := github.NewClient(nil)
	u, _ := url.Parse(srv.URL + "/")
	gh.BaseURL = u

	ss := newFakeScaleSetClient()

	base := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	clk := &testClock{now: base}

	r := newOfflineRunnerReaper(
		gh, ss, "test-org",
		func() []string { return []string{"our-set"} },
		30*time.Minute, time.Hour,
		slog.Default(),
	)
	r.now = clk.Now

	// First tick: records firstSeenOffline; no delete.
	if err := r.sweep(context.Background()); err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	if len(ss.removeRunnerCalls) != 0 {
		t.Errorf("first tick must not delete anything; got %v", ss.removeRunnerCalls)
	}

	// Advance past minAge and sweep again.
	clk.now = base.Add(90 * time.Minute)
	if err := r.sweep(context.Background()); err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if got := ss.removeRunnerCalls; len(got) != 1 || got[0] != 100 {
		t.Errorf("second tick should delete runner 100; got %v", got)
	}
}

func TestOfflineReaper_Sweep_DeletesOnlyEligible(t *testing.T) {
	runners := []*github.Runner{
		newRunner(1, "our-set-aaaaaaaa", "online", false),      // skipped: online
		newRunner(2, "our-set-bbbbbbbb", "offline", false),     // eligible after age
		newRunner(3, "foreign-set-cccccccc", "offline", false), // skipped: foreign
		newRunner(4, "our-set-dddddddd", "offline", true),      // skipped: busy
	}
	srv := newFakeGitHubServer(t, runners)
	t.Cleanup(srv.Close)

	gh := github.NewClient(nil)
	u, _ := url.Parse(srv.URL + "/")
	gh.BaseURL = u
	ss := newFakeScaleSetClient()

	base := time.Now()
	clk := &testClock{now: base}
	r := newOfflineRunnerReaper(
		gh, ss, "test-org",
		func() []string { return []string{"our-set"} },
		30*time.Minute, time.Hour,
		slog.Default(),
	)
	r.now = clk.Now

	_ = r.sweep(context.Background()) // records firstSeen
	clk.now = base.Add(2 * time.Hour)
	_ = r.sweep(context.Background())

	if got := ss.removeRunnerCalls; len(got) != 1 || got[0] != 2 {
		t.Errorf("removeRunnerCalls = %v, want [2]", got)
	}
}

func TestOfflineReaper_Sweep_ListError_NoDeletes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/orgs/test-org/actions/runners", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	gh := github.NewClient(nil)
	u, _ := url.Parse(srv.URL + "/")
	gh.BaseURL = u
	ss := newFakeScaleSetClient()

	r := newOfflineRunnerReaper(
		gh, ss, "test-org",
		func() []string { return []string{"our-set"} },
		30*time.Minute, time.Hour,
		slog.Default(),
	)

	if err := r.sweep(context.Background()); err == nil {
		t.Fatal("expected non-nil error on list failure")
	}
	if len(ss.removeRunnerCalls) != 0 {
		t.Errorf("no deletes expected; got %v", ss.removeRunnerCalls)
	}
}
