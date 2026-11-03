package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAProjectLeadStaysCurrentHoweverLongItIsIdle(t *testing.T) {
	d, reg, fake, _, _, cwd := baseFixture(t)
	for _, id := range []string{"from", "to"} {
		session := fake.sessions[id]
		session.LastActive = time.Now().Add(-5 * 24 * time.Hour)
		fake.sessions[id] = session
		if err := reg.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	d.discoverCatalog(context.Background())
	state, err := d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if row := stateRow(t, state, "to"); row.Current {
		t.Fatalf("an idle session five days old is current before it leads: %+v", row)
	}
	project := stateRow(t, state, "to").Project
	if project == nil {
		t.Fatal("the session has no project")
	}

	if _, err := reg.SetCoordinator(registry.CoordinatorSpec{ProjectID: project.LocalID, ProjectRoot: cwd, Name: "idle-lead", Surface: string(surface.KindCodex), Cwd: cwd}, registry.CoordinatorChange{SessionID: "to"}); err != nil {
		t.Fatal(err)
	}
	d.discoverCatalog(context.Background())
	state, err = d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if row := stateRow(t, state, "to"); !row.Current || row.CurrentReason != "lead" {
		t.Fatalf("the lead is not current: current=%t reason=%q", row.Current, row.CurrentReason)
	}
	if row := stateRow(t, state, "from"); row.Current {
		t.Fatalf("a session that does not lead became current: %+v", row)
	}
	view, err := d.readSessionView(context.Background(), sessionViewRequest{Scope: "current", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range view.Sessions {
		found = found || row.ID == "to" && row.CurrentReason == "lead"
	}
	if !found {
		t.Fatalf("the current view leaves out the lead: %+v", view.Sessions)
	}

	gone := fake.sessions["to"]
	delete(fake.sessions, "to")
	d.discoverCatalog(context.Background())
	if row := storedProjection(t, reg, gone.ID); !row.Current || row.CurrentReason != "lead" {
		t.Fatalf("a lead its runtime no longer lists stopped being current: current=%t reason=%q", row.Current, row.CurrentReason)
	}
}

func TestLeadPresenceKeepsAMoreSpecificReason(t *testing.T) {
	busy := surface.Session{ID: "s", Status: surface.StatusBusy}
	if current, reason := surface.PresenceFor(nil, busy, surface.PresenceInput{Now: time.Now(), Lead: true}); !current || reason != "working" {
		t.Fatalf("a working lead reads %t %q", current, reason)
	}
	if current, reason := surface.UnobservedPresence(2, true); !current || reason != "queued" {
		t.Fatalf("a lead with queued messages reads %t %q", current, reason)
	}
	if current, reason := surface.UnobservedPresence(0, true); !current || reason != "lead" {
		t.Fatalf("an unobserved lead reads %t %q", current, reason)
	}
	if current, _ := surface.UnobservedPresence(0, false); current {
		t.Fatal("an unobserved session that leads nothing is current")
	}
}
