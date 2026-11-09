package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestARowNamesTheAgentThatStartedItOnWhicheverHostItRuns(t *testing.T) {
	_, store := openPresentationRegistry(t)
	for _, id := range []string{"lead", "worker", "mine", "coordinated", "for-a-peer"} {
		recordPresentation(t, store, surface.Session{ID: id, Name: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle}, dashboardSession{})
	}
	hostID := "h0123456789ab"
	if _, err := store.AddHost(registry.PairedHost{ID: hostID, Name: "peer", Endpoint: "https://peer.example", Address: "100.64.0.2", Token: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	d := newTestDaemon(t, store, nil)
	mirror := func(id, startedBy string) {
		raw, _ := json.Marshal(dashboardSession{ID: id, Name: id, Surface: surface.KindCodex, Status: surface.StatusIdle, StartedBy: startedBy})
		session := surface.Session{ID: id, Name: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle}
		if _, _, err := store.ApplyHostCatalogRow(hostID, registry.HostCatalogRow{Session: session, EntityID: hostID + "::" + id, Projection: raw, Project: json.RawMessage(`null`), Checkout: json.RawMessage(`{}`), ObservedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	mirror("peer-lead", "")
	mirror("peer-worker", "peer-lead")
	mirror("worker-of-ours", hosts.Qualify(d.hostIdentity().ID, "lead"))
	for _, origin := range []registry.SessionOrigin{
		registry.OriginOf("worker", "lead"),
		registry.OriginOf("mine", ""),
		{SessionID: "coordinated", Via: registry.OriginCoordinator},
		registry.OriginOf("for-a-peer", "peer-lead"),
	} {
		if err := store.RecordSessionOrigin(origin); err != nil {
			t.Fatal(err)
		}
	}
	for id, want := range map[string]string{
		"worker":                    "lead",
		"mine":                      "",
		"coordinated":               "",
		"lead":                      "",
		"for-a-peer":                hostID + "::peer-lead",
		hostID + "::peer-worker":    hostID + "::peer-lead",
		hostID + "::worker-of-ours": "lead",
	} {
		if got := viewSession(t, d, id).StartedBy; got != want {
			t.Errorf("%s startedBy=%q, want %q", id, got, want)
		}
	}
}
