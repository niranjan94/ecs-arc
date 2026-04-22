package controller

import (
	"context"
	"log/slog"
	"regexp"
	"time"

	"github.com/google/go-github/v61/github"
)

// runnerNameSuffixPattern matches the 8-lowercase-hex suffix the scaler
// appends to every JIT runner name (internal/scaler/scaler.go).
var runnerNameSuffixPattern = regexp.MustCompile(`^(.+)-[0-9a-f]{8}$`)

// offlineRunnerReaper periodically lists the org's Actions runners and
// deregisters ones that (a) match a currently-desired scale set name,
// (b) report status "offline", (c) are not busy, and (d) have been
// continuously observed offline for at least minAge.
type offlineRunnerReaper struct {
	ghClient         *github.Client
	ssClient         ScaleSetClient
	org              string
	desiredNamesFn   func() []string
	interval         time.Duration
	minAge           time.Duration
	logger           *slog.Logger
	firstSeenOffline map[int64]time.Time
	now              func() time.Time // injected for tests
}

func newOfflineRunnerReaper(
	ghClient *github.Client,
	ssClient ScaleSetClient,
	org string,
	desiredNamesFn func() []string,
	interval, minAge time.Duration,
	logger *slog.Logger,
) *offlineRunnerReaper {
	return &offlineRunnerReaper{
		ghClient:         ghClient,
		ssClient:         ssClient,
		org:              org,
		desiredNamesFn:   desiredNamesFn,
		interval:         interval,
		minAge:           minAge,
		logger:           logger,
		firstSeenOffline: make(map[int64]time.Time),
		now:              time.Now,
	}
}

// shouldDelete is a pure decision function: given a runner, the set of
// currently-desired scale set names, when the runner was first seen
// offline, the current time, and the minimum-age threshold, decide
// whether the runner is eligible for deregistration.
func shouldDelete(r *github.Runner, desiredNames []string, firstSeen time.Time, now time.Time, minAge time.Duration) bool {
	if r.GetStatus() != "offline" {
		return false
	}
	if r.GetBusy() {
		return false
	}
	match := runnerNameSuffixPattern.FindStringSubmatch(r.GetName())
	if match == nil {
		return false
	}
	prefix := match[1]
	found := false
	for _, name := range desiredNames {
		if prefix == name {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	if now.Sub(firstSeen) < minAge {
		return false
	}
	return true
}

// sweep performs one pass: list org runners, record/prune firstSeenOffline,
// decide eligibility, delete eligible ones via the scaleset client.
func (r *offlineRunnerReaper) sweep(ctx context.Context) error {
	desired := r.desiredNamesFn()
	runners, err := r.listAllRunners(ctx)
	if err != nil {
		return err
	}

	now := r.now()

	seenIDs := make(map[int64]struct{}, len(runners))
	var stats struct {
		listed, skippedOnline, skippedForeign, skippedBusy, skippedYoung, deleted, failed int
	}

	for _, gr := range runners {
		stats.listed++
		id := gr.GetID()
		seenIDs[id] = struct{}{}

		if gr.GetStatus() != "offline" {
			stats.skippedOnline++
			delete(r.firstSeenOffline, id)
			continue
		}
		if gr.GetBusy() {
			stats.skippedBusy++
			if _, ok := r.firstSeenOffline[id]; !ok {
				r.firstSeenOffline[id] = now
			}
			continue
		}

		if _, ok := r.firstSeenOffline[id]; !ok {
			r.firstSeenOffline[id] = now
		}

		if !shouldDelete(gr, desired, r.firstSeenOffline[id], now, r.minAge) {
			if !nameMatchesAny(gr.GetName(), desired) {
				stats.skippedForeign++
			} else {
				stats.skippedYoung++
			}
			continue
		}

		if err := r.ssClient.RemoveRunner(ctx, id); err != nil {
			stats.failed++
			r.logger.Warn("failed to deregister offline runner",
				slog.Int64("runner_id", id),
				slog.String("runner_name", gr.GetName()),
				slog.String("error", err.Error()),
			)
			continue
		}
		stats.deleted++
		delete(r.firstSeenOffline, id)
		r.logger.Info("deregistered offline runner",
			slog.Int64("runner_id", id),
			slog.String("runner_name", gr.GetName()),
			slog.String("event", "runner_deregistered_offline"),
		)
	}

	for id := range r.firstSeenOffline {
		if _, ok := seenIDs[id]; !ok {
			delete(r.firstSeenOffline, id)
		}
	}

	r.logger.Info("offline runner sweep complete",
		slog.Int("listed", stats.listed),
		slog.Int("skipped_online", stats.skippedOnline),
		slog.Int("skipped_foreign", stats.skippedForeign),
		slog.Int("skipped_busy", stats.skippedBusy),
		slog.Int("skipped_young", stats.skippedYoung),
		slog.Int("deleted", stats.deleted),
		slog.Int("failed", stats.failed),
		slog.String("event", "offline_runner_sweep_complete"),
	)
	return nil
}

func (r *offlineRunnerReaper) listAllRunners(ctx context.Context) ([]*github.Runner, error) {
	var all []*github.Runner
	opts := &github.ListOptions{PerPage: 100}
	for {
		page, resp, err := r.ghClient.Actions.ListOrganizationRunners(ctx, r.org, opts)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Runners...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return all, nil
}

func nameMatchesAny(name string, desired []string) bool {
	m := runnerNameSuffixPattern.FindStringSubmatch(name)
	if m == nil {
		return false
	}
	for _, d := range desired {
		if m[1] == d {
			return true
		}
	}
	return false
}

// Run loops forever until ctx is cancelled, invoking sweep on each tick.
func (r *offlineRunnerReaper) Run(ctx context.Context) {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.sweep(ctx); err != nil {
				r.logger.Error("offline runner sweep failed",
					slog.String("error", err.Error()),
				)
			}
		}
	}
}

