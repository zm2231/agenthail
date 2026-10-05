package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// The registry absorbs a row sharing a transcript once its process exits, so
// the fixture rows use live processes.
var (
	firstPID  = os.Getpid()
	secondPID = os.Getppid()
)

func sharedConversationFixture(t *testing.T) (*Daemon, *registry.Registry, *localStatusSurface) {
	t.Helper()
	d, store, source := localStatusFixture(t)
	source.kind = surface.KindClaude
	first := surface.Session{ID: "first", Surface: surface.KindClaude, Name: "Shared fixture", PID: firstPID, Status: surface.StatusIdle, Transcript: "/fixture/conversation.jsonl", StartedAt: time.UnixMilli(1_700_000_000_000).UTC()}
	alone := surface.Session{ID: "alone", Surface: surface.KindClaude, Name: "Solo fixture", PID: firstPID, Status: surface.StatusIdle, Transcript: "/fixture/other.jsonl", StartedAt: time.UnixMilli(1_700_000_100_000).UTC()}
	source.sessions = map[string]surface.Session{first.ID: first, alone.ID: alone}
	for _, session := range source.sessions {
		if err := store.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
		source.writeStatus(t, session.ID, session.Status)
	}
	return d, store, source
}

func openSecondProcess(t *testing.T, store *registry.Registry, source *localStatusSurface) {
	t.Helper()
	second := surface.Session{ID: "second", Surface: surface.KindClaude, Name: "Shared fixture", PID: secondPID, Status: surface.StatusIdle, Transcript: "/fixture/conversation.jsonl", StartedAt: time.UnixMilli(1_700_000_050_000).UTC()}
	source.sessions[second.ID] = second
	if err := store.RegisterSession(second); err != nil {
		t.Fatal(err)
	}
	source.writeStatus(t, second.ID, second.Status)
}

func catalogSharedEvents(t *testing.T, store *registry.Registry, after uint64, sessionID string) [][]dashboardSharedSession {
	t.Helper()
	window, err := store.CatalogEventsAfter(after, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out [][]dashboardSharedSession
	for _, event := range window.Events {
		if event.Type != "session.upserted" || event.EntityID != sessionID {
			continue
		}
		var envelope struct {
			Session dashboardSession `json:"session"`
		}
		if err := json.Unmarshal(event.Payload, &envelope); err != nil {
			t.Fatal(err)
		}
		out = append(out, envelope.Session.SharedWith)
	}
	return out
}

func stateSession(t *testing.T, d *Daemon, id string) dashboardSession {
	t.Helper()
	state, err := d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range state.Sessions {
		if session.ID == id {
			return session
		}
	}
	t.Fatalf("session %s missing from state", id)
	return dashboardSession{}
}

func detailSharedWith(t *testing.T, d *Daemon, id string) (json.RawMessage, bool) {
	t.Helper()
	response := httptest.NewRecorder()
	d.dashboardSessionHandler(response, httptest.NewRequest(http.MethodGet, "/api/session?id="+id, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("detail %s status=%d body=%s", id, response.Code, response.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	raw, found := body["sharedWith"]
	return raw, found
}

func TestSharedWithIsOmittedForUnsharedConversation(t *testing.T) {
	d, _, _ := sharedConversationFixture(t)
	d.discoverCatalog(context.Background())
	if shared := stateSession(t, d, "first").SharedWith; shared != nil {
		t.Fatalf("unshared row sharedWith=%+v", shared)
	}
	raw, err := json.Marshal(stateSession(t, d, "first"))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, found := fields["sharedWith"]; found {
		t.Fatalf("sharedWith present in %s", raw)
	}
	if _, found := detailSharedWith(t, d, "first"); found {
		t.Fatal("detail carries sharedWith for an unshared conversation")
	}
}

func TestSecondProcessOpeningAndClosingUpdatesBothRows(t *testing.T) {
	d, store, source := sharedConversationFixture(t)
	d.discoverCatalog(context.Background())
	after := latestCatalogSeq(t, store)

	openSecondProcess(t, store, source)
	d.discoverCatalog(context.Background())
	want := map[string]dashboardSharedSession{
		"first":  {ID: "second", Name: "Shared fixture", PID: secondPID, Status: surface.StatusIdle, StartedAt: time.UnixMilli(1_700_000_050_000).UTC()},
		"second": {ID: "first", Name: "Shared fixture", PID: firstPID, Status: surface.StatusIdle, StartedAt: time.UnixMilli(1_700_000_000_000).UTC()},
	}
	for id, peer := range want {
		events := catalogSharedEvents(t, store, after, id)
		if len(events) != 1 || !equalSharedSessions(events[0], []dashboardSharedSession{peer}) {
			t.Fatalf("%s open events=%+v", id, events)
		}
		if shared := stateSession(t, d, id).SharedWith; !equalSharedSessions(shared, []dashboardSharedSession{peer}) {
			t.Fatalf("%s state sharedWith=%+v", id, shared)
		}
		raw, found := detailSharedWith(t, d, id)
		var detail []dashboardSharedSession
		if !found || json.Unmarshal(raw, &detail) != nil || !equalSharedSessions(detail, []dashboardSharedSession{peer}) {
			t.Fatalf("%s detail sharedWith=%s", id, raw)
		}
	}
	if events := catalogSharedEvents(t, store, after, "alone"); len(events) != 0 {
		t.Fatalf("unrelated row republished: %+v", events)
	}

	after = latestCatalogSeq(t, store)
	delete(source.sessions, "second")
	d.discoverCatalog(context.Background())
	if events := catalogSharedEvents(t, store, after, "first"); len(events) != 1 || events[0] != nil {
		t.Fatalf("close events=%+v", events)
	}
	if shared := stateSession(t, d, "first").SharedWith; shared != nil {
		t.Fatalf("state after close sharedWith=%+v", shared)
	}
	if _, found := detailSharedWith(t, d, "first"); found {
		t.Fatal("detail keeps sharedWith after the second process closed")
	}
}

func TestPeerStatusChangeRepublishesSharedRow(t *testing.T) {
	d, store, source := sharedConversationFixture(t)
	openSecondProcess(t, store, source)
	d.discoverCatalog(context.Background())
	after := latestCatalogSeq(t, store)

	source.writeStatus(t, "second", surface.StatusBusy)
	d.refreshCatalogStatus(context.Background())
	events := catalogSharedEvents(t, store, after, "first")
	if len(events) != 1 || len(events[0]) != 1 || events[0][0].Status != surface.StatusBusy {
		t.Fatalf("first events after peer turned busy=%+v", events)
	}
	if statuses := catalogStatusEvents(t, store, after, "second"); len(statuses) != 1 || statuses[0] != string(surface.StatusBusy) {
		t.Fatalf("second status events=%v", statuses)
	}
	raw, _ := detailSharedWith(t, d, "first")
	var detail []dashboardSharedSession
	if json.Unmarshal(raw, &detail) != nil || len(detail) != 1 || detail[0].Status != surface.StatusBusy {
		t.Fatalf("detail sharedWith=%s", raw)
	}
}

func TestSharedClaudeSessionsOrdersPeersByStart(t *testing.T) {
	sessions := []surface.Session{
		{ID: "c", Surface: surface.KindClaude, PID: 3, Transcript: "/t", StartedAt: time.UnixMilli(3)},
		{ID: "a", Surface: surface.KindClaude, PID: 1, Transcript: "/t", StartedAt: time.UnixMilli(1)},
		{ID: "b", Surface: surface.KindClaude, PID: 2, Transcript: "/t", StartedAt: time.UnixMilli(2)},
		{ID: "gone", Surface: surface.KindClaude, Transcript: "/t"},
		{ID: "codex", Surface: surface.KindCodex, PID: 4, Transcript: "/t"},
	}
	shared := sharedClaudeSessions(sessions)
	if len(shared) != 3 {
		t.Fatalf("shared=%+v", shared)
	}
	if got := shared["c"]; len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("c peers=%+v", got)
	}
}

func TestFailedClaudeDiscoveryKeepsSnapshotAndDetailAgreeing(t *testing.T) {
	d, store, source := sharedConversationFixture(t)
	openSecondProcess(t, store, source)
	d.discoverCatalog(context.Background())
	d.Surfaces = []surface.Surface{&failingClaudeListSurface{daemonSurface: source.daemonSurface}}
	d.discoverCatalog(context.Background())
	for id, peer := range map[string]string{"first": "second", "second": "first"} {
		row := stateSession(t, d, id)
		if row.Freshness == nil || !row.Freshness.Stale {
			t.Fatalf("%s freshness=%+v after failed discovery", id, row.Freshness)
		}
		raw, found := detailSharedWith(t, d, id)
		var detail []dashboardSharedSession
		if !found || json.Unmarshal(raw, &detail) != nil || !equalSharedSessions(detail, row.SharedWith) || len(detail) != 1 || detail[0].ID != peer {
			t.Fatalf("%s snapshot sharedWith=%+v detail=%s", id, row.SharedWith, raw)
		}
	}
}

func TestPeerThatDidNotCommitIsDroppedFromSharedRow(t *testing.T) {
	d, store, source := sharedConversationFixture(t)
	openSecondProcess(t, store, source)
	d.discoverCatalog(context.Background())
	after := latestCatalogSeq(t, store)
	// discoverCatalog drops a row whose publish failed from the live set.
	delete(d.catalogLive, "second")
	d.refreshCatalogStatus(context.Background())
	if events := catalogSharedEvents(t, store, after, "first"); len(events) != 1 || events[0] != nil {
		t.Fatalf("first events=%+v", events)
	}
	if shared := stateSession(t, d, "first").SharedWith; shared != nil {
		t.Fatalf("state sharedWith=%+v", shared)
	}
	if raw, found := detailSharedWith(t, d, "first"); found {
		t.Fatalf("detail sharedWith=%s", raw)
	}
	after = latestCatalogSeq(t, store)
	d.refreshCatalogStatus(context.Background())
	if events := catalogSharedEvents(t, store, after, "first"); len(events) != 0 {
		t.Fatalf("settled relation republished: %+v", events)
	}
	d.discoverCatalog(context.Background())
	if shared := stateSession(t, d, "first").SharedWith; len(shared) != 1 || shared[0].ID != "second" {
		t.Fatalf("relation not restored by the next discovery: %+v", shared)
	}
}
