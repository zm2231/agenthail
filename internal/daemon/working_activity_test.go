package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/daemonapi"
	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/projectresolve"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/claudesurface"
	"github.com/zm2231/agenthail/internal/surface/codexsurface"
)

// A session card's Working timer counts from busySince. A turn that worked
// an hour, waited three hours on the person, then worked ten minutes reads
// 1h 10m, and a snooze until the turn finishes outlasts the answer.
func TestTheWorkingTimeOfATurnLeavesOutItsWaitsOnThePerson(t *testing.T) {
	_, store, _, _, _ := daemonFixture(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	claude := claudesurface.NewClaude(nil, t.TempDir())
	codex := codexsurface.NewCodex(unreachableDesktop)
	d := newTestDaemon(t, store, []surface.Surface{claude, codex})
	now := time.Now().UTC().Truncate(time.Millisecond)
	start := now.Add(-4*time.Hour - 10*time.Minute)
	for _, tc := range []struct {
		adapter surface.Surface
		session surface.Session
	}{
		{claude, surface.Session{ID: "claude-turn", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost}},
		{codex, surface.Session{ID: "codex-turn", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop}},
	} {
		id := tc.session.ID
		if err := store.RecordObservedSession(tc.session); err != nil {
			t.Fatal(err)
		}
		wait := func(at time.Time, waits ...registry.Wait) {
			t.Helper()
			if _, err := store.SyncWaits(id, waits, "", at); err != nil {
				t.Fatal(err)
			}
		}
		wait(start.Add(-2*time.Hour), registry.Wait{Ref: "earlier-turn", Kind: surface.WaitingQuestion, Since: start.Add(-2 * time.Hour)})
		wait(start.Add(-time.Hour))
		d.syncWaits(tc.session, &surface.Waiting{Kind: surface.WaitingQuestion, ID: "question", Since: start.Add(time.Hour), Answerable: true,
			Open: []string{"question", "approval"}, OpenSince: map[string]time.Time{"question": start.Add(time.Hour), "approval": start.Add(2 * time.Hour)}})
		wait(start.Add(2*time.Hour+30*time.Minute), registry.Wait{Ref: "approval", Answerable: true})
		wait(start.Add(4 * time.Hour))

		session := tc.session
		session.Status = surface.StatusBusy
		session.Activity = &surface.Activity{BusySince: start, LastProgressAt: now}
		row := d.catalogSessionProjection(tc.adapter, session, projectresolve.CatalogIdentity{}, now, "", 0, nil, false, false, dashconfig.Config{}, d.turnWaited(session))
		if row.Activity == nil {
			t.Fatalf("%s: the working row has no activity", id)
		}
		if worked := now.Sub(row.Activity.BusySince); worked != time.Hour+10*time.Minute {
			t.Errorf("%s: the timer reads %s, want 1h10m", id, worked)
		}
		if !row.Activity.TurnStartedAt.Equal(start) {
			t.Errorf("%s: turnStartedAt %s, want the turn's start %s", id, row.Activity.TurnStartedAt, start)
		}

		raw, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		var saved dashboardSession
		if err := json.Unmarshal(raw, &saved); err != nil {
			t.Fatal(err)
		}
		saved.Display = &daemonapi.SessionDisplay{Kind: "working"}
		saved.Snooze = &dashboardSnooze{SnoozedAt: start.Add(30 * time.Minute), Wake: registry.SnoozeWakeFinished}
		if got := sessionInbox(saved, now); got != InboxSnoozed {
			t.Errorf("%s: a snooze until the turn finished woke when the person answered: %q", id, got)
		}
	}
}
