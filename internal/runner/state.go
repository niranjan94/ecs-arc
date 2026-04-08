package runner

import "sync"

// State tracks runner tasks and their lifecycle (idle -> busy -> done).
// It is safe for concurrent use.
type State struct {
	mu   sync.Mutex
	idle map[string]string // runner name -> ECS task ARN
	busy map[string]string // runner name -> ECS task ARN
}

// NewState creates a new empty State.
func NewState() *State {
	return &State{
		idle: make(map[string]string),
		busy: make(map[string]string),
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
