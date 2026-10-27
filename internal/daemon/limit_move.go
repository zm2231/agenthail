package daemon

import (
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/claudepick"
	"github.com/zm2231/agenthail/internal/registry"
)

const limitMoveOfferTTL = 5 * time.Second

type limitMoveOffer struct {
	Root    string `json:"root"`
	Account string `json:"account,omitempty"`
	Reason  string `json:"reason"`
}

type limitMoves struct {
	mu      sync.Mutex
	offers  map[string]*limitMoveOffer
	readAt  time.Time
	fresh   bool
	tried   map[string]bool
	movedAt map[string]time.Time
}

func newLimitMoves() *limitMoves {
	return &limitMoves{tried: map[string]bool{}, movedAt: map[string]time.Time{}}
}

func (m *limitMoves) forget() {
	m.mu.Lock()
	m.fresh = false
	m.mu.Unlock()
}

func limitMoveKey(stop registry.LimitResume) string {
	return stop.SessionID + ":" + stop.TurnID
}

func (m *limitMoves) prune(stops []registry.LimitResume, now time.Time) {
	waiting := map[string]bool{}
	for _, stop := range stops {
		waiting[limitMoveKey(stop)] = stop.State == registry.LimitResumeWaiting
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key := range m.tried {
		if !waiting[key] {
			delete(m.tried, key)
		}
	}
	for root, at := range m.movedAt {
		if now.Sub(at) >= limitResumeSpacing {
			delete(m.movedAt, root)
		}
	}
}

func (d *Daemon) limitMoveOffer(sessionID string, now time.Time) *limitMoveOffer {
	return d.cachedLimitMoveOffers(now)[sessionID]
}

func (d *Daemon) cachedLimitMoveOffers(now time.Time) map[string]*limitMoveOffer {
	moves := d.limitResumes.moves
	moves.mu.Lock()
	if moves.fresh && now.Sub(moves.readAt) < limitMoveOfferTTL {
		offers := moves.offers
		moves.mu.Unlock()
		return offers
	}
	moves.mu.Unlock()
	stops, err := d.Registry.LimitResumes()
	if err != nil {
		return nil
	}
	offers := d.limitMoveOffers(stops, now)
	moves.mu.Lock()
	moves.offers, moves.readAt, moves.fresh = offers, now, true
	moves.mu.Unlock()
	return offers
}

func (d *Daemon) limitMoveOffers(stops []registry.LimitResume, now time.Time) map[string]*limitMoveOffer {
	waiting := false
	for _, stop := range stops {
		waiting = waiting || stop.State == registry.LimitResumeWaiting
	}
	if !waiting {
		return nil
	}
	roots := d.currentClaudeConfigRoots()
	if len(roots) < 2 {
		return nil
	}
	policy := d.claudeAccountPolicy()
	candidates := claudePickCandidates(d.claudeCachedUsageSets(roots, now), nil, now)
	offers := map[string]*limitMoveOffer{}
	for _, stop := range stops {
		if stop.State != registry.LimitResumeWaiting {
			continue
		}
		if target, ok := claudepick.MoveTarget(candidates, stop.AccountKey, policy); ok {
			offers[stop.SessionID] = &limitMoveOffer{Root: target.Name, Account: target.Account, Reason: target.Reason}
		}
	}
	return offers
}

func (d *Daemon) claudeAccountPolicy() claudepick.Policy {
	config, err := LoadDashboardConfig()
	if err != nil {
		return claudepick.DefaultPolicy()
	}
	return config.ClaudeAccountPolicy()
}

func sameLimitMoveOffer(a, b *limitMoveOffer) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
