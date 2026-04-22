package runner

import (
	"testing"
	"time"
)

func TestState_AddAndCount(t *testing.T) {
	s := NewState()
	s.AddIdle("runner-1", "task-arn-1")
	s.AddIdle("runner-2", "task-arn-2")

	if got := s.Count(); got != 2 {
		t.Errorf("Count() = %d, want 2", got)
	}
}

func TestState_MarkBusy(t *testing.T) {
	s := NewState()
	s.AddIdle("runner-1", "task-arn-1")
	s.MarkBusy("runner-1")

	if got := s.Count(); got != 1 {
		t.Errorf("Count() = %d, want 1", got)
	}
	if s.IdleCount() != 0 {
		t.Errorf("IdleCount() = %d, want 0", s.IdleCount())
	}
}

func TestState_MarkDone(t *testing.T) {
	s := NewState()
	s.AddIdle("runner-1", "task-arn-1")
	s.MarkBusy("runner-1")

	taskARN := s.MarkDone("runner-1")
	if taskARN != "task-arn-1" {
		t.Errorf("MarkDone() = %q, want %q", taskARN, "task-arn-1")
	}
	if got := s.Count(); got != 0 {
		t.Errorf("Count() = %d, want 0", got)
	}
}

func TestState_MarkDone_IdleRunner(t *testing.T) {
	s := NewState()
	s.AddIdle("runner-1", "task-arn-1")

	taskARN := s.MarkDone("runner-1")
	if taskARN != "task-arn-1" {
		t.Errorf("MarkDone() = %q, want %q", taskARN, "task-arn-1")
	}
}

func TestState_MarkDone_Unknown(t *testing.T) {
	s := NewState()
	taskARN := s.MarkDone("nonexistent")
	if taskARN != "" {
		t.Errorf("MarkDone(unknown) = %q, want empty string", taskARN)
	}
}

func TestState_MarkDeregistered_IsDeregistered(t *testing.T) {
	s := NewState()
	if s.IsDeregistered("r1") {
		t.Fatal("fresh state should not mark r1 deregistered")
	}
	s.MarkDeregistered("r1")
	if !s.IsDeregistered("r1") {
		t.Fatal("MarkDeregistered should make IsDeregistered return true")
	}
	if s.IsDeregistered("r2") {
		t.Fatal("IsDeregistered should not false-positive for other names")
	}
}

func TestState_Deregistered_ExpiresAfterTTL(t *testing.T) {
	s := NewState()
	s.MarkDeregistered("r1")
	// Force-age the entry.
	s.mu.Lock()
	s.deregistered["r1"] = time.Now().Add(-2 * deregisteredTTL)
	s.mu.Unlock()
	if s.IsDeregistered("r1") {
		t.Fatal("expired entry should not report deregistered")
	}
	// And the prune should have removed it.
	s.mu.Lock()
	_, present := s.deregistered["r1"]
	s.mu.Unlock()
	if present {
		t.Fatal("expired entry should have been pruned on read")
	}
}
