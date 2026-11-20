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
	mu     sync.Mutex
	offers map[string]*limitMoveOffer
	readAt time.Time
	fresh  bool
}

func newLimitMoves() *limitMoves {
	return &limitMoves{}
}

func (m *limitMoves) forget() {
	m.mu.Lock()
	m.fresh = false
	m.mu.Unlock()
}

func (d *Daemon) limitMoveOffer(sessionID string, now time.Time) *limitMoveOffer {
	return d.cachedLimitMoveOffers(d.readProxyView(now), now)[sessionID]
}

func (d *Daemon) cachedLimitMoveOffers(view proxyView, now time.Time) map[string]*limitMoveOffer {
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
	offers := d.limitMoveOffers(view, stops, now)
	moves.mu.Lock()
	moves.offers, moves.readAt, moves.fresh = offers, now, true
	moves.mu.Unlock()
	return offers
}

func (d *Daemon) limitMoveOffers(view proxyView, stops []registry.LimitResume, now time.Time) map[string]*limitMoveOffer {
	waiting := false
	for _, stop := range stops {
		waiting = waiting || stop.State == registry.LimitResumeWaiting
	}
	if !waiting {
		return nil
	}
	if len(view.roots) < 2 {
		return nil
	}
	candidates := limitMoveCandidates(d.claudeCachedUsageSets(view.roots, now), now)
	offers := map[string]*limitMoveOffer{}
	for _, stop := range stops {
		if stop.State != registry.LimitResumeWaiting {
			continue
		}
		if root, ok := view.rootWithRoom(stop.AccountKey, d.sessionIsFable(stop.SessionID), now); ok {
			offers[stop.SessionID] = &limitMoveOffer{Root: root.Name, Reason: "the " + root.Proxy + " proxy has room"}
			continue
		}
		if target, ok := claudepick.MoveTarget(candidates, stop.AccountKey); ok {
			offers[stop.SessionID] = &limitMoveOffer{Root: target.Name, Account: target.Account, Reason: target.Reason}
		}
	}
	return offers
}

func sameLimitMoveOffer(a, b *limitMoveOffer) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
