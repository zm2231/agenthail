package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestARowNamesTheAgentThatStartedItAndNoOtherStarter(t *testing.T) {
	_, store := openPresentationRegistry(t)
	for _, id := range []string{"lead", "worker", "mine", "coordinated"} {
		recordPresentation(t, store, surface.Session{ID: id, Name: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle}, dashboardSession{})
	}
	for _, origin := range []registry.SessionOrigin{
		registry.OriginOf("worker", "lead"),
		registry.OriginOf("mine", ""),
		{SessionID: "coordinated", Via: registry.OriginCoordinator},
	} {
		if err := store.RecordSessionOrigin(origin); err != nil {
			t.Fatal(err)
		}
	}
	hostID := "h0123456789ab"
	if _, err := store.AddHost(registry.PairedHost{ID: hostID, Name: "peer", Endpoint: "https://peer.example", Address: "100.64.0.2", Token: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(dashboardSession{ID: "remote-worker", Name: "remote-worker", Surface: surface.KindCodex, Status: surface.StatusIdle, StartedBy: "remote-lead"})
	remote := surface.Session{ID: "remote-worker", Name: "remote-worker", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle}
	if _, _, err := store.ApplyHostCatalogRow(hostID, registry.HostCatalogRow{Session: remote, EntityID: hostID + "::remote-worker", Projection: raw, Project: json.RawMessage(`null`), Checkout: json.RawMessage(`{}`), ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	d := newTestDaemon(t, store, nil)
	for id, want := range map[string]string{"worker": "lead", "mine": "", "coordinated": "", "lead": "", hostID + "::remote-worker": hostID + "::remote-lead"} {
		if got := viewSession(t, d, id).StartedBy; got != want {
			t.Errorf("%s startedBy=%q, want %q", id, got, want)
		}
	}
}
