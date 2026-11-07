package daemon

import (
	"slices"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const idleCompactionHoldsKept = 50

type IdleCompactionHold struct {
	SessionID     string    `json:"sessionId"`
	Root          string    `json:"root"`
	RequestAt     time.Time `json:"requestAt"`
	ContextTokens int64     `json:"contextTokens"`
	Reason        string    `json:"reason"`
	At            time.Time `json:"at"`
}

type idleCompactionHolds struct {
	mu        sync.Mutex
	announced map[string]time.Time
	recent    []IdleCompactionHold
}

func idleCompactionHold(session *surface.Session, settings surface.IdleCompaction, refused, canCompact bool) string {
	switch {
	case !settings.Enabled:
		return "idle compaction is off for this root"
	case session.Status != surface.StatusIdle:
		return "the session is " + string(session.Status) + ", not idle"
	case session.WaitingOn != nil:
		return "the session waits on " + string(session.WaitingOn.Kind)
	case session.BackgroundTasks != nil && session.BackgroundTasks.Running:
		return "a background task is running"
	case refused:
		return "the account under this root is at a usage limit"
	case !canCompact:
		return "the session cannot compact: a terminal without Remote Control"
	}
	return ""
}

func (h *idleCompactionHolds) record(hold IdleCompactionHold, announce func(IdleCompactionHold)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if at, ok := h.announced[hold.SessionID]; ok && at.Equal(hold.RequestAt) {
		return
	}
	if h.announced == nil {
		h.announced = map[string]time.Time{}
	}
	h.announced[hold.SessionID] = hold.RequestAt
	h.recent = append(h.recent, hold)
	if len(h.recent) > idleCompactionHoldsKept {
		h.recent = slices.Delete(h.recent, 0, len(h.recent)-idleCompactionHoldsKept)
	}
	announce(hold)
}

func (h *idleCompactionHolds) forgetExcept(listed map[string]bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id := range h.announced {
		if !listed[id] {
			delete(h.announced, id)
		}
	}
}

func (h *idleCompactionHolds) list() []IdleCompactionHold {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.recent)
}
