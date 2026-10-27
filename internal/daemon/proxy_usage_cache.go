package daemon

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zm2231/agenthail/internal/proxyacct"
	"github.com/zm2231/agenthail/internal/proxyacct/cliproxyapi"
)

const (
	proxyUsageTTL     = time.Minute
	proxyUsageTimeout = proxyacct.KeyReadLimit + 8*time.Second
)

type proxyUsageCache struct {
	mu      sync.Mutex
	entries map[string]*proxyUsageEntry
}

type proxyUsageEntry struct {
	mu         sync.Mutex
	client     proxyacct.Proxy
	readAt     time.Time
	accounts   []proxyacct.Account
	err        error
	last       atomic.Pointer[proxyReading]
	refreshing atomic.Bool
	writing    sync.Mutex
	marksMu    sync.Mutex
	marks      map[string]proxyMark
}

type proxyMark struct {
	value float64
	host  string
	at    time.Time
}

type proxyReading struct {
	accounts []proxyacct.Account
	readAt   time.Time
	err      error
}

var newProxyClient = func(config proxyacct.Config) proxyacct.Proxy {
	return cliproxyapi.New(config.URL, config.Management, nil)
}

// A changed config gets a new client, so a refused-key pause ends when the
// key reference changes.
func (c *proxyUsageCache) entry(config proxyacct.Config) *proxyUsageEntry {
	identity, _ := json.Marshal(config)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]*proxyUsageEntry{}
	}
	entry, ok := c.entries[string(identity)]
	if !ok {
		entry = &proxyUsageEntry{client: newProxyClient(config)}
		c.entries[string(identity)] = entry
	}
	return entry
}

func (c *proxyUsageCache) retain(configs []proxyacct.Config) {
	keep := map[string]bool{}
	for _, config := range configs {
		identity, _ := json.Marshal(config)
		keep[string(identity)] = true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for identity := range c.entries {
		if !keep[identity] {
			delete(c.entries, identity)
		}
	}
}

func (c *proxyUsageCache) read(ctx context.Context, config proxyacct.Config, now time.Time) proxyReading {
	entry := c.entry(config)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.readAt.IsZero() || now.Sub(entry.readAt) >= proxyUsageTTL {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), proxyUsageTimeout)
		entry.accounts, entry.err = nil, entry.client.Health(ctx)
		if entry.err == nil {
			entry.accounts, entry.err = entry.client.Accounts(ctx)
		}
		cancel()
		entry.readAt = now
		entry.last.Store(&proxyReading{accounts: entry.accounts, readAt: entry.readAt, err: entry.err})
	}
	return proxyReading{accounts: entry.accounts, readAt: entry.readAt, err: entry.err}
}

func (c *proxyUsageCache) fresh(ctx context.Context, config proxyacct.Config) proxyReading {
	entry := c.entry(config)
	entry.mu.Lock()
	entry.readAt = time.Time{}
	entry.mu.Unlock()
	return c.read(ctx, config, time.Now())
}

func (e *proxyUsageEntry) mark(account string, mark proxyMark) {
	e.marksMu.Lock()
	defer e.marksMu.Unlock()
	if e.marks == nil {
		e.marks = map[string]proxyMark{}
	}
	e.marks[account] = mark
}

// A mark holds only while the proxy still has the value Agenthail wrote.
func (e *proxyUsageEntry) markFor(account string, preference float64) (proxyMark, bool) {
	e.marksMu.Lock()
	defer e.marksMu.Unlock()
	mark, ok := e.marks[account]
	return mark, ok && mark.value == preference
}

func (c *proxyUsageCache) cached(config proxyacct.Config, now time.Time) *proxyReading {
	entry := c.entry(config)
	last := entry.last.Load()
	if (last == nil || now.Sub(last.readAt) >= proxyUsageTTL) && entry.refreshing.CompareAndSwap(false, true) {
		go func() {
			defer entry.refreshing.Store(false)
			c.read(context.Background(), config, time.Now())
		}()
	}
	return last
}
