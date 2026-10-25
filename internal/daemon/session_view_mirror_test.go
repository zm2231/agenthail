package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestPresentationMirrorsQualifyMembersAndReformatStatus(t *testing.T) {
	d, store, _, _, _ := daemonFixture(t)
	hostID := "h0123456789ab"
	if _, err := store.AddHost(registry.PairedHost{ID: hostID, Name: "peer", Endpoint: "https://peer.example", Address: "100.64.0.2", Token: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"root", "child"} {
		session := surface.Session{ID: id, Name: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle}
		row := dashboardSession{ID: id, Name: id, Surface: session.Surface, Status: session.Status, Host: &dashboardHost{ID: hostID, Name: "peer", State: "connected"}, UnavailableReason: "workspace missing", Display: &SessionDisplay{Label: "Peer text"}}
		if id == "child" {
			session.Subagent = &surface.Subagent{ParentID: "root", RootID: "root"}
			row.Subagent = session.Subagent
			row.WaitingOn = &surface.Waiting{ID: "request", Answerable: true}
		}
		raw, _ := json.Marshal(row)
		if _, _, err := store.ApplyHostCatalogRow(hostID, registry.HostCatalogRow{Session: session, EntityID: hostID + "::" + id, Projection: raw, Project: json.RawMessage(`null`), Checkout: json.RawMessage(`{}`), ObservedAt: time.Now(), UnavailableReason: row.UnavailableReason}); err != nil {
			t.Fatal(err)
		}
	}
	state, err := d.readSessionView(context.Background(), sessionViewRequest{Scope: "current", Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	if state.View.Counts["current"] != (SessionViewCount{1, 1}) || len(state.View.Families) != 1 || state.View.Families[0].RootID != hostID+"::root" || state.View.Families[0].Members[1].ID != hostID+"::child" {
		t.Fatalf("view=%+v", state.View)
	}
	member, err := d.readSessionView(context.Background(), sessionViewRequest{Scope: "current", Limit: 200, Revision: state.View.Revision, IDs: []string{hostID + "::child"}})
	if err != nil || len(member.Sessions) != 1 || member.Sessions[0].Display.Label != "Waiting on you" || member.Sessions[0].Subagent.ParentID != hostID+"::root" {
		t.Fatalf("member=%+v err=%v", member, err)
	}
}

func TestPresentationOldIdleLimitIsCurrentAndPinned(t *testing.T) {
	d, store, _, _, _ := daemonFixture(t)
	row := dashboardSession{ID: "limited", Name: "Limited", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle, UsageLimit: &surface.UsageLimit{}}
	raw, _ := json.Marshal(row)
	if _, _, err := store.RecordCatalogSession(registry.CatalogSessionState{Session: surface.Session{ID: row.ID, Name: row.Name, Surface: row.Surface, RuntimeKind: row.RuntimeKind, Status: row.Status, UsageLimit: row.UsageLimit}, Project: json.RawMessage(`null`), Checkout: json.RawMessage(`{}`), ObservedAt: time.Now(), ProjectionFingerprint: string(raw)}, registry.CatalogEvent{DedupeKey: "limited", Type: "session.upserted", EntityID: row.ID, Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	state, err := d.readSessionView(context.Background(), sessionViewRequest{Scope: "current", Limit: 200, Filters: SessionViewFilters{Attention: true}})
	if err != nil || len(state.Sessions) != 1 || state.Sessions[0].Attention.Kind != "usageLimit" || !state.View.Families[0].Pinned {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}
