package surfaces

import (
	"maps"
	"slices"
	"sync"
)

// codexSubscriptions are this process's app-server connections that resumed a
// thread, which a reload drops and restores, since Codex takes a resume's
// config only once no connection is subscribed.
type codexSubscriptions struct {
	mu       sync.Mutex
	byThread map[string]map[codexClient]bool
	gates    map[string]*threadGate
}

type threadGate struct {
	sync.RWMutex
	users int
}

type failingClient interface{ failed() bool }

func (s *codexSubscriptions) gate(threadID string) *threadGate {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gates == nil {
		s.gates = map[string]*threadGate{}
	}
	gate := s.gates[threadID]
	if gate == nil {
		gate = &threadGate{}
		s.gates[threadID] = gate
	}
	gate.users++
	return gate
}

func (s *codexSubscriptions) leave(threadID string, gate *threadGate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gate.users--; gate.users == 0 {
		delete(s.gates, threadID)
	}
}

func (s *codexSubscriptions) shared(threadID string) func() {
	gate := s.gate(threadID)
	gate.RLock()
	return func() { gate.RUnlock(); s.leave(threadID, gate) }
}

func (s *codexSubscriptions) exclusive(threadID string) func() {
	gate := s.gate(threadID)
	gate.Lock()
	return func() { gate.Unlock(); s.leave(threadID, gate) }
}

func (s *codexSubscriptions) add(threadID string, client codexClient) {
	if _, ok := client.(failingClient); !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byThread == nil {
		s.byThread = map[string]map[codexClient]bool{}
	}
	if s.byThread[threadID] == nil {
		s.byThread[threadID] = map[codexClient]bool{}
	}
	s.byThread[threadID][client] = true
	s.prune(threadID)
}

func (s *codexSubscriptions) prune(threadID string) {
	for client := range s.byThread[threadID] {
		if client.(failingClient).failed() {
			delete(s.byThread[threadID], client)
		}
	}
	if len(s.byThread[threadID]) == 0 {
		delete(s.byThread, threadID)
	}
}

func (s *codexSubscriptions) remove(threadID string, client codexClient) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byThread[threadID], client)
	if len(s.byThread[threadID]) == 0 {
		delete(s.byThread, threadID)
	}
}

func (s *codexSubscriptions) clients(threadID string) []codexClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(threadID)
	return slices.Collect(maps.Keys(s.byThread[threadID]))
}
