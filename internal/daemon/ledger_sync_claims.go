package daemon

import (
	"sync"
	"time"
)

const ledgerClaimTTL = 10 * time.Minute

type pendingLedgerClaim struct {
	requester string
	expires   time.Time
}

type ledgerClaims struct {
	mu        sync.Mutex
	byProject map[string]pendingLedgerClaim
	now       func() time.Time
}

func (c *ledgerClaims) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *ledgerClaims) liveLocked(key string) (pendingLedgerClaim, bool) {
	claim, ok := c.byProject[key]
	if ok && !c.clock().Before(claim.expires) {
		delete(c.byProject, key)
		return pendingLedgerClaim{}, false
	}
	return claim, ok
}

func (c *ledgerClaims) reserve(key, requester string) (holder pendingLedgerClaim, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if claim, live := c.liveLocked(key); live && claim.requester != requester {
		return claim, false
	}
	if c.byProject == nil {
		c.byProject = map[string]pendingLedgerClaim{}
	}
	c.byProject[key] = pendingLedgerClaim{requester: requester, expires: c.clock().Add(ledgerClaimTTL)}
	return pendingLedgerClaim{}, true
}

func (c *ledgerClaims) lookup(key string) (pendingLedgerClaim, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.liveLocked(key)
}

func (c *ledgerClaims) release(key, requester string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if claim, ok := c.byProject[key]; ok && claim.requester == requester {
		delete(c.byProject, key)
	}
}
