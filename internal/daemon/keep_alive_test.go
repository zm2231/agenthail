package daemon

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type keptSurface struct {
	*daemonSurface
	ended   map[string]bool
	wakeErr error
	woken   []string
}

func (k *keptSurface) Ended(_ context.Context, sess *surface.Session) bool { return k.ended[sess.ID] }

func (k *keptSurface) Wake(_ context.Context, sess *surface.Session) error {
	k.woken = append(k.woken, sess.ID)
	if k.wakeErr != nil {
		return k.wakeErr
	}
	k.ended[sess.ID] = false
	return nil
}

func (k *keptSurface) SessionAction(_ context.Context, sess *surface.Session, action string) (map[string]any, error) {
	k.ended[sess.ID] = action == "stop"
	return map[string]any{"sessionId": sess.ID, "action": action}, nil
}

func keptFixture(t *testing.T) (*Daemon, *registry.Registry, *keptSurface) {
	t.Helper()
	d, r, fake, _, _ := daemonFixture(t)
	kept := &keptSurface{daemonSurface: fake, ended: map[string]bool{}}
	d.Surfaces = []surface.Surface{kept}
	return d, r, kept
}

func pinAction(t *testing.T, d *Daemon, action, id string) {
	t.Helper()
	if w := serveDashboardRequest(dashboardRouter(d), http.MethodPost, "/api/action", `{"action":"`+action+`","sessionId":"`+id+`"}`); w.Code != http.StatusOK {
		t.Fatalf("%s %s: %d %s", action, id, w.Code, w.Body.String())
	}
}

func TestAPinnedSessionThatEndsIsBroughtBackOnceWithoutATurn(t *testing.T) {
	d, _, kept := keptFixture(t)
	pinAction(t, d, "session-pin", "to")
	kept.ended["to"], kept.ended["from"] = true, true
	now := time.Now()
	d.keepPinned(context.Background(), now)
	d.keepPinned(context.Background(), now.Add(keepAliveEvery))
	if strings.Join(kept.woken, ",") != "to" {
		t.Fatalf("woken %v; only the pinned session, once", kept.woken)
	}
	if len(kept.sent) != 0 || len(kept.startOptions) != 0 {
		t.Fatalf("bringing a session back sent %v and started %v", kept.sent, kept.startOptions)
	}

	restarted := &keptSurface{daemonSurface: kept.daemonSurface, ended: map[string]bool{"to": true}}
	newTestDaemon(t, d.Registry, []surface.Surface{restarted}).keepPinned(context.Background(), now.Add(time.Minute))
	if len(restarted.woken) != 1 {
		t.Fatalf("after a restart the pinned session was woken %d times", len(restarted.woken))
	}
}

func TestAnArchivedStoppedOrUnpinnedSessionIsLeftDown(t *testing.T) {
	d, _, kept := keptFixture(t)
	pinAction(t, d, "session-pin", "to")
	pinAction(t, d, "session-pin", "from")
	pinAction(t, d, "session-archive", "from")
	pinAction(t, d, "session-lifecycle-stop", "to")
	now := time.Now()
	d.keepPinned(context.Background(), now)
	if len(kept.woken) != 0 {
		t.Fatalf("woke %v: archived and stopped sessions stay down", kept.woken)
	}

	pinAction(t, d, "session-lifecycle-resume", "to")
	kept.ended["to"] = true
	d.keepPinned(context.Background(), now)
	if strings.Join(kept.woken, ",") != "to" {
		t.Fatalf("woke %v after the person resumed it", kept.woken)
	}

	pinAction(t, d, "session-unpin", "to")
	kept.ended["to"] = true
	d.keepPinned(context.Background(), now.Add(time.Hour))
	if len(kept.woken) != 1 {
		t.Fatalf("an unpinned session was woken: %v", kept.woken)
	}
}

func TestAPinnedSessionThatCannotBeBroughtBackBacksOffThenNeedsThePersonOnce(t *testing.T) {
	d, r, kept := keptFixture(t)
	pinAction(t, d, "session-pin", "to")
	kept.ended["to"] = true
	kept.wakeErr = errors.New("Claude refused to resume")
	start := time.Now()
	at := start
	var waits []time.Duration
	for pass := 0; pass < 40; pass++ {
		before := len(kept.woken)
		d.keepPinned(context.Background(), at)
		if len(kept.woken) > before && len(kept.woken) > 1 {
			waits = append(waits, at.Sub(start))
			start = at
		}
		at = at.Add(keepAliveEvery)
	}
	if len(kept.woken) != len(keepAliveBackoff) {
		t.Fatalf("tried %d times, want %d then stop", len(kept.woken), len(keepAliveBackoff))
	}
	for i, wait := range waits {
		if wait != keepAliveBackoff[i] {
			t.Fatalf("wait %d was %s, want %s (%v)", i, wait, keepAliveBackoff[i], waits)
		}
	}
	pinned, err := r.PinnedSessions()
	if err != nil || len(pinned) != 1 || pinned[0].KeepAlive.FailedAt.IsZero() || !strings.Contains(pinned[0].KeepAlive.Error, "refused") {
		t.Fatalf("pinned=%+v err=%v", pinned, err)
	}
	row := keptRow(t, d, "to")
	if row.KeepAlive == nil || row.Attention == nil || row.Attention.Kind != AttentionKeepAliveFailed {
		t.Fatalf("row keepAlive=%+v attention=%+v", row.KeepAlive, row.Attention)
	}

	kept.wakeErr = nil
	pinAction(t, d, "session-lifecycle-resume", "to")
	kept.ended["to"] = true
	d.keepPinned(context.Background(), at)
	if len(kept.woken) != len(keepAliveBackoff)+1 {
		t.Fatalf("the person's resume did not start the keeper over: %d wakes", len(kept.woken))
	}
}

func TestKeepingCountsASessionThatKeepsEndingAndForgetsItAfterTenHealthyMinutes(t *testing.T) {
	now := time.Now()
	state := registry.KeepAlive{}
	for i := 0; i < len(keepAliveBackoff)-1; i++ {
		state = keptAttempt(state, nil, now)
		if state.FailedAt.IsZero() != true || !state.NextAttempt.Equal(now.Add(keepAliveBackoff[i])) {
			t.Fatalf("attempt %d: %+v", i+1, state)
		}
	}
	if failed := keptAttempt(state, nil, now); failed.FailedAt.IsZero() {
		t.Fatalf("a session woken %d times in %s was still kept", len(keepAliveBackoff), keepAliveWindow)
	}
	if fresh := keptAttempt(state, nil, now.Add(keepAliveWindow+time.Second)); fresh.Attempts != 1 {
		t.Fatalf("attempts outside the window counted: %+v", fresh)
	}
	healthy := keptHealthy(state, now)
	if healthy.HealthySince.IsZero() || keptHealthy(healthy, now.Add(keepAliveHealthy-time.Second)) == (registry.KeepAlive{}) {
		t.Fatalf("forgot too early: %+v", healthy)
	}
	if keptHealthy(healthy, now.Add(keepAliveHealthy)) != (registry.KeepAlive{}) {
		t.Fatal("ten healthy minutes did not reset the keeper")
	}
}

func keptRow(t *testing.T, d *Daemon, id string) dashboardSession {
	t.Helper()
	d.discoverCatalog(context.Background())
	view, err := d.readSessionView(context.Background(), sessionViewRequest{Scope: "all", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range view.Sessions {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("%s is not in the session view", id)
	return dashboardSession{}
}
