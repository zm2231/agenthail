package daemon

import (
	"maps"
	"slices"
	"sync"
	"time"
)

// ledgerChangeHub publishes one change per project at most once per wait, so replaying thousands of events sends a handful.
// A subscriber keeps only the newest version per project, so one that reads slowly gets the current versions and is never dropped.
type ledgerChangeHub struct {
	mu          sync.Mutex
	wait        time.Duration
	version     func(key string) (string, error)
	logf        func(format string, args ...any)
	pending     map[string]bool
	subscribers map[uint64]*ledgerSubscriber
	nextID      uint64
}

type ledgerSubscriber struct {
	wake   chan struct{}
	unread map[string]string
}

func newLedgerChangeHub(wait time.Duration, version func(string) (string, error), logf func(string, ...any)) *ledgerChangeHub {
	return &ledgerChangeHub{wait: wait, version: version, logf: logf, pending: map[string]bool{}, subscribers: map[uint64]*ledgerSubscriber{}}
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
	for _, sub := range h.subscribers {
		sub.unread[key] = version
		select {
		case sub.wake <- struct{}{}:
		default:
		}
	}
}

func (h *ledgerChangeHub) subscribe() (uint64, <-chan struct{}, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	id, sub := h.nextID, &ledgerSubscriber{wake: make(chan struct{}, 1), unread: map[string]string{}}
	h.subscribers[id] = sub
	return id, sub.wake, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.subscribers, id)
	}
}

func (h *ledgerChangeHub) take(id uint64) []ledgerChange {
	h.mu.Lock()
	defer h.mu.Unlock()
	sub := h.subscribers[id]
	if sub == nil {
		return nil
	}
	changes := make([]ledgerChange, 0, len(sub.unread))
	for _, key := range slices.Sorted(maps.Keys(sub.unread)) {
		changes = append(changes, ledgerChange{ProjectKey: key, Version: sub.unread[key]})
	}
	clear(sub.unread)
	return changes
}
