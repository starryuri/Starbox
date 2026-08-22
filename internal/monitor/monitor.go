package monitor

import (
	"sync"
)

// State holds the latest textual snapshot of every task output.
// Each task updates its own slot; the HTTP endpoint reads a full copy.
type State struct {
	mu   sync.RWMutex
	vals map[string]string
}

func New() *State {
	return &State{vals: make(map[string]string)}
}

func (s *State) Set(id, v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vals[id] = v
}

func (s *State) Get(id string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.vals[id]
}

func (s *State) Snapshot() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.vals))
	for k, v := range s.vals {
		out[k] = v
	}
	return out
}
