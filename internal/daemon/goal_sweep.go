package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	goalSweepEvery  = 30 * time.Second
	goalSweepWindow = 24 * time.Hour
)

type goalSweeper struct {
	mu       sync.Mutex
	seen     map[string]time.Time
	notified map[string]string
}

func newGoalSweeper() *goalSweeper {
	return &goalSweeper{seen: map[string]time.Time{}, notified: map[string]string{}}
}

func (g *goalSweeper) due(session surface.Session, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !session.LastActive.IsZero() && now.Sub(session.LastActive) <= goalSweepWindow && session.LastActive.After(g.seen[session.ID])
}

func (g *goalSweeper) read(session surface.Session) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.seen[session.ID] = session.LastActive
}

func (g *goalSweeper) firstNotice(sessionID, key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.notified[sessionID] == key {
		return false
	}
	g.notified[sessionID] = key
	return true
}

func (d *Daemon) runGoalSweeps(ctx context.Context) {
	ticker := time.NewTicker(goalSweepEvery)
	defer ticker.Stop()
	d.sweepStoppedGoals(ctx, time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			d.sweepStoppedGoals(ctx, now)
		}
	}
}

func (d *Daemon) sweepStoppedGoals(ctx context.Context, now time.Time) {
	sessions, err := d.Registry.ListSessions(0)
	if err != nil {
		d.log.Printf("list sessions for stopped goals: %s", err)
		return
	}
	routed := map[string]bool{}
	if routes, err := d.Registry.ListRoutes(); err == nil {
		for _, route := range routes {
			if route.Active && route.Handoff == registry.HandoffGoalCompleted {
				routed[route.FromSession] = true
			}
		}
	}
	for i := range sessions {
		session := sessions[i]
		if routed[session.ID] || session.Status == surface.StatusBusy || !d.goalSweep.due(session, now) {
			continue
		}
		if d.readStoppedGoal(ctx, session) {
			d.goalSweep.read(session)
		}
	}
}

func (d *Daemon) readStoppedGoal(ctx context.Context, session surface.Session) bool {
	adapter, err := d.adapters().For(&session)
	if err != nil {
		return false
	}
	if !surface.EffectiveCapabilities(adapter, &session).Goal {
		return true
	}
	goalCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	goal, err := adapter.GoalGet(goalCtx, &session)
	cancel()
	if err != nil {
		return false
	}
	if goal == nil || !goalStopped(goal.Status) || goal.CreatedAt.IsZero() {
		return true
	}
	return d.noticeStoppedGoal(ctx, session, goal) == nil
}

func goalStopNoticeKey(sessionID string, goal *surface.GoalState) string {
	sum := sha256.Sum256([]byte(sessionID + "\x00" + goalKeyOf(goal)))
	return "goal-stop-" + hex.EncodeToString(sum[:16])
}

// The person is told before the keyed agent notice is queued, since that key is what ends the retries.
func (d *Daemon) noticeStoppedGoal(ctx context.Context, session surface.Session, goal *surface.GoalState) error {
	key := goalStopNoticeKey(session.ID, goal)
	name := d.resolveDisplay(session.ID)
	note := goalStopNote(d.answerTarget(session.ID), goal)
	to, ok := d.noticeRecipient(session)
	if ok {
		if used, err := d.Registry.DeliveryKeyUsed(key); err != nil || used {
			return err
		}
	}
	if d.goalSweep.firstNotice(session.ID, key) {
		d.notifyStoppedGoal(ctx, session.ID, key, fmt.Sprintf("%s: %s", name, note))
	}
	if !ok {
		return nil
	}
	message := fmt.Sprintf("[agenthail] %s stopped working on its goal. %s The person was notified too.", name, note)
	if _, err := d.sendNotice(to, session, message, key); err != nil {
		d.log.Printf("tell the %s that %s stopped on its goal: %s", to.role, name, err)
		return err
	}
	return nil
}

func (d *Daemon) notifyStoppedGoal(ctx context.Context, sessionID, key, message string) {
	if err := d.notifyDesktop("Agenthail", message, sessionID, "agenthail-"+key, "", nil); err != nil {
		d.log.Printf("desktop notification: %s", err)
	}
	notifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	d.notifyPairedDevices(notifyCtx, mobileNotification{Title: "Agenthail", Message: message, SessionID: sessionID, EventType: "session.goal.stopped", CollapseID: key})
}
