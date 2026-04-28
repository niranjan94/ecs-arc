package controller

import (
	"context"
	"log/slog"
	"time"
)

// listenSession runs one attempt of the upstream listener. It must block
// until either the parent context is cancelled (returning ctx.Err()) or a
// recoverable error occurs that requires re-establishing the message session.
type listenSession func(ctx context.Context) error

// backoffFunc returns the wait duration before the next listener-session
// attempt. The argument is the 1-based attempt number that just failed.
type backoffFunc func(attempt int) time.Duration

// runListenerWithReconnect repeatedly invokes run, treating any non-context
// return as a transient failure and reconnecting after backoff. It returns
// only when the supplied context is cancelled.
//
// Background: the upstream actions/scaleset listener bubbles up any error
// from its long-poll cycle (including transient broker glitches such as a
// 200 OK with an empty body that fails to decode). Without this loop, a
// single such error terminated the per-scale-set goroutine and the affected
// scale set went offline until the controller process restarted.
func runListenerWithReconnect(
	ctx context.Context,
	run listenSession,
	logger *slog.Logger,
	backoff backoffFunc,
) error {
	for attempt := 1; ; attempt++ {
		err := run(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		wait := backoff(attempt)
		if err != nil {
			logger.Warn("listener session ended, reconnecting",
				slog.Int("attempt", attempt),
				slog.Duration("backoff", wait),
				slog.String("error", err.Error()),
				slog.String("event", "listener_reconnect"),
			)
		} else {
			logger.Warn("listener session returned without error, reconnecting",
				slog.Int("attempt", attempt),
				slog.Duration("backoff", wait),
				slog.String("event", "listener_reconnect"),
			)
		}

		if wait > 0 {
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
		} else {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}
	}
}

// listenerBackoffSchedule lists the base wait per consecutive failed
// listener attempt. Calls beyond the slice length reuse the last entry.
var listenerBackoffSchedule = []time.Duration{
	5 * time.Second,
	15 * time.Second,
	30 * time.Second,
	60 * time.Second,
}

// defaultListenerBackoff returns the canonical backoff used in production.
// It is also used as the seam for tests that want to swap in a fast schedule.
func defaultListenerBackoff(attempt int) time.Duration {
	if attempt < 1 {
		return listenerBackoffSchedule[0]
	}
	idx := attempt - 1
	if idx >= len(listenerBackoffSchedule) {
		idx = len(listenerBackoffSchedule) - 1
	}
	return listenerBackoffSchedule[idx]
}

