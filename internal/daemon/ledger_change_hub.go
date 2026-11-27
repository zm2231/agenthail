package daemon

import (
	"maps"
	"slices"
	"sync"
	"time"
)

// ledgerChangeHub publishes one change per project at most once per wait, so replaying thousands of events sends a handful.
// A subscriber keeps only the newest version per project, so one that reads slowly gets the current versions and is never dropped.
// Publishes of one project can overlap when reading its version takes longer than the wait; only the one that started last
// delivers, so a slow earlier read never replaces a newer version. A failed read is retried after the wait, so a committed
// change reaches the stream without waiting for another write.
type ledgerChangeHub struct {
	mu          sync.Mutex
	wait        time.Duration
	version     func(key string) (string, error)
	logf        func(format string, args ...any)
	pending     map[string]bool
	latest      map[string]uint64
	started     uint64
	subscribers map[uint64]*ledgerSubscriber
	nextID      uint64
}

type ledgerSubscriber struct {
	only   string
	wake   chan struct{}
	unread map[string]string
}

func (sub *ledgerSubscriber) wants(key string) bool {
	return sub.only == "" || sub.only == key
}

func newLedgerChangeHub(wait time.Duration, version func(string) (string, error), logf func(string, ...any)) *ledgerChangeHub {
	return &ledgerChangeHub{wait: wait, version: version, logf: logf, pending: map[string]bool{}, latest: map[string]uint64{}, subscribers: map[uint64]*ledgerSubscriber{}}
}

func (h *ledgerChangeHub) changed(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subscribers) > 0 {
		h.schedule(key)
	}
}

func (h *ledgerChangeHub) schedule(key string) {
	if h.pending[key] {
		return
	}
	h.pending[key] = true
	time.AfterFunc(h.wait, func() { h.publish(key) })
}

func (h *ledgerChangeHub) publish(key string) {
	h.mu.Lock()
	delete(h.pending, key)
	h.started++
	publish := h.started
	h.latest[key] = publish
	listening := false
	for _, sub := range h.subscribers {
		listening = listening || sub.wants(key)
	}
	h.mu.Unlock()
	if !listening {
		return
	}
	version, err := h.version(key)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.latest[key] != publish {
		return
	}
	if err != nil {
		h.logf("ledger events: version of %s: %s; retrying", key, err)
		h.schedule(key)
		return
	}
	for _, sub := range h.subscribers {
		if !sub.wants(key) {
			continue
		}
		sub.unread[key] = version
		select {
		case sub.wake <- struct{}{}:
		default:
		}
	}
}

func (h *ledgerChangeHub) subscribe(only string) (uint64, <-chan struct{}, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	id, sub := h.nextID, &ledgerSubscriber{only: only, wake: make(chan struct{}, 1), unread: map[string]string{}}
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
