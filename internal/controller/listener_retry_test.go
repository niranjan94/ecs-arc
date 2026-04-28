package controller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestRunListenerWithReconnect_RetriesOnError verifies that when the inner
// listener-session function returns a non-context error, the loop reinvokes it
// rather than letting the goroutine die. This is the regression test for the
// "scale set goes offline until controller restart" bug, where a single
// transient error from the upstream listener (e.g. broker EOF on a 200
// response) terminated the per-scale-set goroutine permanently.
func TestRunListenerWithReconnect_RetriesOnError(t *testing.T) {
	var calls atomic.Int32
	thirdAttempt := make(chan struct{})

	run := func(ctx context.Context) error {
		n := calls.Add(1)
		if n < 3 {
			return errors.New("transient broker decode error")
		}
		close(thirdAttempt)
		<-ctx.Done()
		return ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runListenerWithReconnect(ctx, run, discardLogger(), func(int) time.Duration { return 0 })
	}()

	select {
	case <-thirdAttempt:
	case <-time.After(2 * time.Second):
		t.Fatalf("listener did not reach 3rd attempt; calls=%d", calls.Load())
	}

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runListenerWithReconnect did not return after cancel")
	}

	if got := calls.Load(); got != 3 {
		t.Fatalf("want 3 calls, got %d", got)
	}
}

// TestRunListenerWithReconnect_ReturnsWhenCtxCancelledMidSession verifies that
// when the inner session function returns ctx.Err() because the parent
// cancelled, the loop returns immediately rather than retrying.
func TestRunListenerWithReconnect_ReturnsWhenCtxCancelledMidSession(t *testing.T) {
	var calls atomic.Int32
	run := func(ctx context.Context) error {
		calls.Add(1)
		<-ctx.Done()
		return ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	err := runListenerWithReconnect(ctx, run, discardLogger(), func(int) time.Duration { return 0 })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("want exactly 1 call, got %d", got)
	}
}

// TestRunListenerWithReconnect_AppliesBackoff verifies that the loop sleeps
// for the duration returned by the backoff function between attempts, and
// passes the attempt number (1-based) so callers can implement progressive
// backoff schedules.
func TestRunListenerWithReconnect_AppliesBackoff(t *testing.T) {
	var (
		mu       sync.Mutex
		attempts []int
	)
	backoff := func(attempt int) time.Duration {
		mu.Lock()
		attempts = append(attempts, attempt)
		mu.Unlock()
		return 0
	}

	var calls atomic.Int32
	run := func(ctx context.Context) error {
		n := calls.Add(1)
		if n >= 3 {
			<-ctx.Done()
			return ctx.Err()
		}
		return errors.New("transient")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runListenerWithReconnect(ctx, run, discardLogger(), backoff)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if calls.Load() >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(attempts) < 2 {
		t.Fatalf("want backoff invoked at least twice, got %v", attempts)
	}
	for i, a := range attempts {
		if a != i+1 {
			t.Fatalf("attempts not 1-based monotonic: %v", attempts)
		}
	}
}
