package daemon

import (
	"sync"
	"time"
)

const ledgerChangeBuffer = 64

// ledgerChangeHub publishes one change per project at most once per wait, after the burst that started it, so replaying thousands of events sends a handful.
type ledgerChangeHub struct {
	mu          sync.Mutex
	wait        time.Duration
	version     func(key string) (string, error)
	logf        func(format string, args ...any)
	pending     map[string]bool
	subscribers map[uint64]chan ledgerChange
	nextID      uint64
}

func newLedgerChangeHub(wait time.Duration, version func(string) (string, error), logf func(string, ...any)) *ledgerChangeHub {
	return &ledgerChangeHub{wait: wait, version: version, logf: logf, pending: map[string]bool{}, subscribers: map[uint64]chan ledgerChange{}}
}

func (h *ledgerChangeHub) changed(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pending[key] || len(h.subscribers) == 0 {
		return
	}
	h.pending[key] = true
	time.AfterFunc(h.wait, func() { h.publish(key) })
}

func (h *ledgerChangeHub) publish(key string) {
	h.mu.Lock()
	delete(h.pending, key)
	listening := len(h.subscribers) > 0
	h.mu.Unlock()
	if !listening {
		return
	}
	version, err := h.version(key)
	if err != nil {
		h.logf("ledger events: version of %s: %s", key, err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, ch := range h.subscribers {
		select {
		case ch <- ledgerChange{ProjectKey: key, Version: version}:
		default:
			// A reader this far behind refetches everything when it reconnects.
			close(ch)
			delete(h.subscribers, id)
		}
	}
}

func (h *ledgerChangeHub) subscribe() (<-chan ledgerChange, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	id, ch := h.nextID, make(chan ledgerChange, ledgerChangeBuffer)
	h.subscribers[id] = ch
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.subscribers[id]; ok {
			close(ch)
			delete(h.subscribers, id)
		}
	}
}
