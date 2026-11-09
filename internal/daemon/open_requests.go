package daemon

import (
	"slices"
	"sync"

	"github.com/zm2231/agenthail/internal/surface"
)

// The registry records only the oldest request a session waits on; the rest
// of its open requests are kept here, from the last observation.
type openRequestSets struct {
	mu        sync.Mutex
	bySession map[string][]string
}

// keep stores what observed lists open behind recorded, and reports whether
// that differs from what was kept.
func (o *openRequestSets) keep(sessionID string, observed, recorded *surface.Waiting) bool {
	var open []string
	if observed != nil && recorded != nil && observed.ID == recorded.ID {
		open = slices.Clone(observed.Open)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if slices.Equal(o.bySession[sessionID], open) {
		return false
	}
	if open == nil {
		delete(o.bySession, sessionID)
		return true
	}
	if o.bySession == nil {
		o.bySession = map[string][]string{}
	}
	o.bySession[sessionID] = open
	return true
}

func (o *openRequestSets) attach(sessionID string, waiting *surface.Waiting) *surface.Waiting {
	if waiting == nil {
		return nil
	}
	o.mu.Lock()
	open := slices.Clone(o.bySession[sessionID])
	o.mu.Unlock()
	attached := *waiting
	attached.Open = open
	return &attached
}

// agrees reports whether a listing's open requests, when it names any, are
// the ones kept.
func (o *openRequestSets) agrees(sessionID string, listed *surface.Waiting) bool {
	if listed == nil || listed.Open == nil {
		return true
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Equal(o.bySession[sessionID], listed.Open)
}
