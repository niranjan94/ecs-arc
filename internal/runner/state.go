package runner

import (
	"sync"
	"time"
)

// deregisteredTTL bounds how long a name stays in the deregistered set.
// Layer 1 (scaler) and layer 2 (reaper) consult this set to dedupe
// RemoveRunner calls; once ECS has stopped listing the task (~1h after
// stop), layer 2 stops observing it and the entry can be forgotten.
const deregisteredTTL = 1 * time.Hour

// State tracks runner tasks and their lifecycle (idle -> busy -> done).
// It is safe for concurrent use.
type State struct {
	mu           sync.Mutex
	idle         map[string]string    // runner name -> ECS task ARN
	busy         map[string]string    // runner name -> ECS task ARN
	deregistered map[string]time.Time // runner name -> when marked
}

// NewState creates a new empty State.
func NewState() *State {
	return &State{
		idle:         make(map[string]string),
		busy:         make(map[string]string),
		deregistered: make(map[string]time.Time),
	}
}

// AddIdle registers a new runner as idle.
func (s *State) AddIdle(name, taskARN string) {
	s.mu.Lock()
	s.idle[name] = taskARN
	s.mu.Unlock()
}

// MarkBusy transitions a runner from idle to busy.
func (s *State) MarkBusy(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if taskARN, ok := s.idle[name]; ok {
		delete(s.idle, name)
		s.busy[name] = taskARN
	}
}

// MarkDone removes a runner from tracking entirely, returning its task ARN.
// Returns empty string if the runner is unknown.
func (s *State) MarkDone(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if taskARN, ok := s.busy[name]; ok {
		delete(s.busy, name)
		return taskARN
	}
	if taskARN, ok := s.idle[name]; ok {
		delete(s.idle, name)
		return taskARN
	}
	return ""
}

// Count returns the total number of tracked runners (idle + busy).
func (s *State) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.idle) + len(s.busy)
}

// IdleCount returns the number of idle runners.
func (s *State) IdleCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.idle)
}

// MarkDeregistered records that name has been successfully deregistered
// on the GitHub side. Subsequent IsDeregistered calls return true until
// the entry expires after deregisteredTTL.
func (s *State) MarkDeregistered(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneDeregisteredLocked(time.Now())
	s.deregistered[name] = time.Now()
}

// IsDeregistered reports whether name was recently marked deregistered.
// It prunes expired entries as a side effect.
func (s *State) IsDeregistered(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneDeregisteredLocked(time.Now())
	_, ok := s.deregistered[name]
	return ok
}

func (s *State) pruneDeregisteredLocked(now time.Time) {
	for name, t := range s.deregistered {
		if now.Sub(t) >= deregisteredTTL {
			delete(s.deregistered, name)
		}
	}
}
