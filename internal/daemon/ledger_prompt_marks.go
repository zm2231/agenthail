package daemon

import (
	"sync"
	"time"
)

const promptMarkTTL = 10 * time.Minute

// Prompt ids the transcript showed were not the person's, for a hook capture that arrives after the journal.
type promptMarks struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func (m *promptMarks) add(promptID string, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seen == nil {
		m.seen = map[string]time.Time{}
	}
	for id, at := range m.seen {
		if now.Sub(at) > promptMarkTTL {
			delete(m.seen, id)
		}
	}
	m.seen[promptID] = now
}

func (m *promptMarks) has(promptID string, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	at, ok := m.seen[promptID]
	return ok && now.Sub(at) <= promptMarkTTL
}
