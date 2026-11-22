package usage

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zm2231/agenthail/internal/proxyacct"
	"github.com/zm2231/agenthail/internal/proxyacct/cliproxyapi"
)

const ProxyTTL = time.Minute

const proxyRequestTimeout = 10 * time.Second

// RequestTimeout and KeyReadLimit default to 10 seconds and
// proxyacct.KeyReadLimit when zero.
type ProxyCache struct {
	RequestTimeout time.Duration
	KeyReadLimit   time.Duration

	mu      sync.Mutex
	entries map[string]*ProxyEntry
}

func (c *ProxyCache) requestTimeout() time.Duration {
	if c.RequestTimeout > 0 {
		return c.RequestTimeout
	}
	return proxyRequestTimeout
}

// KeyedTimeout bounds a request that first reads the management key.
func (c *ProxyCache) KeyedTimeout() time.Duration {
	keyRead := proxyacct.KeyReadLimit
	if c.KeyReadLimit > 0 {
		keyRead = c.KeyReadLimit
	}
	return keyRead + c.requestTimeout()
}

type ProxyEntry struct {
	mu         sync.Mutex
	client     proxyacct.Proxy
	readAt     time.Time
	accounts   []proxyacct.Account
	err        error
	last       atomic.Pointer[ProxyReading]
	refreshing atomic.Bool
	Writing    sync.Mutex
	marksMu    sync.Mutex
	marks      map[string]ProxyMark
}

type ProxyMark struct {
	Value float64
	Host  string
	At    time.Time
}

type ProxyReading struct {
	Accounts []proxyacct.Account
	ReadAt   time.Time
	Err      error
}

// A changed config gets a new client, so a refused-key pause ends when the
// key reference changes.
func (c *ProxyCache) Entry(config proxyacct.Config) *ProxyEntry {
	identity, _ := json.Marshal(config)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]*ProxyEntry{}
	}
	entry, ok := c.entries[string(identity)]
	if !ok {
		entry = &ProxyEntry{client: cliproxyapi.New(config.URL, config.Management, nil)}
		c.entries[string(identity)] = entry
	}
	return entry
}

func (c *ProxyCache) Retain(configs []proxyacct.Config) {
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

func (c *ProxyCache) Read(ctx context.Context, config proxyacct.Config, now time.Time) ProxyReading {
	entry := c.Entry(config)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.readAt.IsZero() || now.Sub(entry.readAt) >= ProxyTTL {
		base := context.WithoutCancel(ctx)
		health, cancel := context.WithTimeout(base, c.requestTimeout())
		entry.accounts, entry.err = nil, entry.client.Health(health)
		cancel()
		if entry.err == nil {
			keyed, cancel := context.WithTimeout(base, c.KeyedTimeout())
			entry.accounts, entry.err = entry.client.Accounts(keyed)
			cancel()
		}
		entry.readAt = now
		entry.last.Store(&ProxyReading{Accounts: entry.accounts, ReadAt: entry.readAt, Err: entry.err})
	}
	return ProxyReading{Accounts: entry.accounts, ReadAt: entry.readAt, Err: entry.err}
}

func (c *ProxyCache) Fresh(ctx context.Context, config proxyacct.Config) ProxyReading {
	entry := c.Entry(config)
	entry.mu.Lock()
	entry.readAt = time.Time{}
	entry.mu.Unlock()
	return c.Read(ctx, config, time.Now())
}

func (e *ProxyEntry) Client() proxyacct.Proxy { return e.client }

func (e *ProxyEntry) Last() *ProxyReading { return e.last.Load() }

func (e *ProxyEntry) Mark(account string, mark ProxyMark) {
	e.marksMu.Lock()
	defer e.marksMu.Unlock()
	if e.marks == nil {
		e.marks = map[string]ProxyMark{}
	}
	e.marks[account] = mark
}

// A mark holds only while the proxy still has the value Agenthail wrote.
func (e *ProxyEntry) MarkFor(account string, preference float64) (ProxyMark, bool) {
	e.marksMu.Lock()
	defer e.marksMu.Unlock()
	mark, ok := e.marks[account]
	return mark, ok && mark.Value == preference
}

func (c *ProxyCache) Cached(config proxyacct.Config, now time.Time) *ProxyReading {
	entry := c.Entry(config)
	last := entry.last.Load()
	if (last == nil || now.Sub(last.ReadAt) >= ProxyTTL) && entry.refreshing.CompareAndSwap(false, true) {
		go func() {
			defer entry.refreshing.Store(false)
			c.Read(context.Background(), config, time.Now())
		}()
	}
	return last
}
