package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestSettingAndReplacingALeadPublishesBothRowsAndMovesTheBaseTag(t *testing.T) {
	d, reg, fake, _, _, _ := baseFixture(t)
	for _, id := range []string{"from", "to"} {
		session := fake.sessions[id]
		session.LastActive = time.Now().Add(-5 * 24 * time.Hour)
		fake.sessions[id] = session
		if err := reg.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	d.discoverCatalog(context.Background())
	d.syncLeads()
	handler := d.dashboardHandler(&dashboardServer{token: dashboardTestToken})
	base := func(tag string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot?sessions=none", nil)
		request.Header.Set("Authorization", "Bearer "+dashboardTestToken)
		if tag != "" {
			request.Header.Set("If-None-Match", tag)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	tag := base("").Header().Get("ETag")
	if held := base(tag); held.Code != http.StatusNotModified {
		t.Fatalf("an unchanged base answered %d", held.Code)
	}
	lead := func(id string) bool {
		row := storedProjection(t, reg, id)
		return row.Current && row.CurrentReason == "lead"
	}

	if set := postAction(t, handler, map[string]any{"action": "coordinator-set", "sessionId": "to", "name": "lead-a"}); set.Code != http.StatusOK {
		t.Fatalf("set: %d %s", set.Code, set.Body.String())
	}
	if !lead("to") || lead("from") {
		t.Fatalf("after set, to leads=%t from leads=%t", lead("to"), lead("from"))
	}
	after := base(tag)
	if after.Code != http.StatusOK || after.Header().Get("ETag") == tag {
		t.Fatalf("a new lead kept the base tag: %d", after.Code)
	}
	tag = after.Header().Get("ETag")

	if replaced := postAction(t, handler, map[string]any{"action": "coordinator-set", "sessionId": "from", "name": "lead-a", "replace": true}); replaced.Code != http.StatusOK {
		t.Fatalf("replace: %d %s", replaced.Code, replaced.Body.String())
	}
	if !lead("from") || lead("to") {
		t.Fatalf("after replace, from leads=%t to leads=%t", lead("from"), lead("to"))
	}
	if moved := base(tag); moved.Code != http.StatusOK || moved.Header().Get("ETag") == tag {
		t.Fatalf("a replaced lead kept the base tag: %d", moved.Code)
	}
}

func TestALeadNamedOutsideTheDaemonIsPublishedOnTheNextStatusTick(t *testing.T) {
	d, reg, fake, _, _, cwd := baseFixture(t)
	session := fake.sessions["to"]
	session.LastActive = time.Now().Add(-5 * 24 * time.Hour)
	fake.sessions["to"] = session
	if err := reg.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	d.discoverCatalog(context.Background())
	d.syncLeads()
	project := storedProjection(t, reg, "to").Project
	if _, err := reg.SetCoordinator(registry.CoordinatorSpec{ProjectID: project.LocalID, ProjectRoot: cwd, Name: "cli-lead", Surface: string(surface.KindCodex), Cwd: cwd}, registry.CoordinatorChange{SessionID: "to"}); err != nil {
		t.Fatal(err)
	}
	if row := storedProjection(t, reg, "to"); row.CurrentReason == "lead" {
		t.Fatal("the lead was published before the daemon looked")
	}
	d.syncLeads()
	if row := storedProjection(t, reg, "to"); !row.Current || row.CurrentReason != "lead" {
		t.Fatalf("a lead written outside the daemon stayed unpublished: %t %q", row.Current, row.CurrentReason)
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
