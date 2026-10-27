package daemon

import (
	"sync"

	"github.com/zm2231/agenthail/internal/registry"
)

type limitMoveClaims struct {
	mu     sync.Mutex
	moving map[string]bool
}

func (d *Daemon) claimLimitMove(sessionID, turnID string) (claimed, moving bool) {
	claims := &d.limitResumes.claims
	claims.mu.Lock()
	defer claims.mu.Unlock()
	if claims.moving[sessionID] {
		return false, true
	}
	stop, found, err := d.Registry.LimitResumeFor(sessionID)
	if err != nil || !found || stop.State != registry.LimitResumeWaiting || turnID != "" && stop.TurnID != turnID {
		return false, false
	}
	if claims.moving == nil {
		claims.moving = map[string]bool{}
	}
	claims.moving[sessionID] = true
	return true, false
}

func (d *Daemon) unclaimLimitMove(sessionID string) {
	claims := &d.limitResumes.claims
	claims.mu.Lock()
	delete(claims.moving, sessionID)
	claims.mu.Unlock()
}

func (d *Daemon) unlessLimitMoving(sessionID string, change func()) bool {
	claims := &d.limitResumes.claims
	claims.mu.Lock()
	defer claims.mu.Unlock()
	if claims.moving[sessionID] {
		return false
	}
	change()
	return true
}
