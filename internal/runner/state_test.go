package runner

import (
	"testing"
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
