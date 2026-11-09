package daemon

import (
	"context"
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
	record, err := store.AddHost(registry.PairedHost{ID: "h0123456789ab", Name: "peer", Endpoint: "https://peer.example", Address: "100.64.0.2", Token: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	d := newTestDaemon(t, store, nil)
	peer := &pairedHost{record: record}
	for id, startedBy := range map[string]string{"peer-lead": "", "peer-worker": "peer-lead", "worker-of-ours": hosts.Qualify(d.hostIdentity().ID, "lead")} {
		if err := d.mirrorRemoteRow(peer, dashboardSession{ID: id, Name: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, StartedBy: startedBy}, false); err != nil {
			t.Fatal(err)
		}
	}
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
		"worker":                       "lead",
		"mine":                         "",
		"coordinated":                  "",
		"lead":                         "",
		"for-a-peer":                   record.ID + "::peer-lead",
		record.ID + "::peer-worker":    record.ID + "::peer-lead",
		record.ID + "::worker-of-ours": "lead",
	} {
		if got := viewSession(t, d, id).StartedBy; got != want {
			t.Errorf("%s startedBy=%q, want %q", id, got, want)
		}
	}
}

func TestAStarterRecordedAfterTheRowIsPublishedReachesEveryRead(t *testing.T) {
	d, r, fake, parent, child := daemonFixture(t)
	for _, session := range []surface.Session{parent, child} {
		session.LastActive = time.Now()
		fake.sessions[session.ID] = session
	}
	d.discoverCatalog(context.Background())
	if got := viewSession(t, d, child.ID).StartedBy; got != "" {
		t.Fatalf("setup: startedBy=%q before any origin", got)
	}
	if err := r.RecordSessionOrigin(registry.OriginOf(child.ID, parent.ID)); err != nil {
		t.Fatal(err)
	}
	if got := viewSession(t, d, child.ID).StartedBy; got != parent.ID {
		t.Fatalf("the cached view kept startedBy=%q, want %q", got, parent.ID)
	}
	d.discoverCatalog(context.Background())
	fingerprint, _, err := r.CatalogProjectionFingerprint(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	var published dashboardSession
	if err := json.Unmarshal([]byte(fingerprint), &published); err != nil || published.StartedBy != parent.ID {
		t.Fatalf("the published row has startedBy=%q (%v), want %q", published.StartedBy, err, parent.ID)
	}
	state, err := d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range state.Sessions {
		if row.ID == child.ID && row.StartedBy != parent.ID {
			t.Fatalf("the unscoped snapshot has startedBy=%q, want %q", row.StartedBy, parent.ID)
		}
	}
}
