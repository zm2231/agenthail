package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestRecentPeerRegistrationReadsFreshCatalogRowsOnly(t *testing.T) {
	reg, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	codex := &daemonSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{}}
	notion := &daemonSurface{kind: surface.KindNotion, sessions: map[string]surface.Session{}}
	claude := &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{}}
	d := New(reg, []surface.Surface{codex, notion, claude})
	now := time.Now()
	for _, session := range []surface.Session{
		{ID: "recent", Surface: surface.KindCodex, Transport: "managed", Status: surface.StatusIdle, LastActive: now},
		{ID: "last-page", Surface: surface.KindCodex, Transport: "readOnly", Status: surface.StatusIdle, LastActive: now.Add(-48 * time.Hour)},
		{ID: "offline", Surface: surface.KindCodex, Status: surface.StatusOffline, LastActive: now},
		{ID: "notes", Surface: surface.KindNotion, Status: surface.StatusIdle, LastActive: now},
		{ID: "native", Surface: surface.KindClaude, Status: surface.StatusIdle, LastActive: now},
		{ID: "unconfigured", Surface: surface.SurfaceKind("other"), Status: surface.StatusBusy, LastActive: now},
	} {
		if _, _, err := reg.RecordCatalogSession(registry.CatalogSessionState{Session: session, HostProject: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: now.UTC(), ProjectionFingerprint: `{}`}, registry.CatalogEvent{DedupeKey: "session.upserted:" + session.ID, Type: "session.upserted", EntityID: session.ID, Payload: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := reg.MarkCatalogDiscoveryFailure(surface.KindNotion, "catalog discovery failed", now); err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	d.registerRecentClaudePeers(context.Background(), func(ctx context.Context, id string) error {
		seen[id]++
		return nil
	})
	if len(seen) != 1 || seen["recent"] != 1 {
		t.Fatalf("registered=%v", seen)
	}
	if codex.listCalls.Load()+notion.listCalls.Load()+claude.listCalls.Load() != 0 {
		t.Fatal("peer registration listed surfaces instead of reading the catalog")
	}
}

func TestClaudePeerEligibilityKeepsBusyAndRecentOnly(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name    string
		session surface.Session
		want    bool
	}{
		{name: "busy old", session: surface.Session{ID: "busy", Status: surface.StatusBusy, LastActive: now.Add(-30 * 24 * time.Hour)}, want: true},
		{name: "recent idle", session: surface.Session{ID: "recent", Status: surface.StatusIdle, LastActive: now.Add(-time.Hour)}, want: true},
		{name: "old idle", session: surface.Session{ID: "old", Status: surface.StatusIdle, LastActive: now.Add(-25 * time.Hour)}},
		{name: "unknown timestamp", session: surface.Session{ID: "unknown", Status: surface.StatusUnknown}},
		{name: "offline", session: surface.Session{ID: "offline", Status: surface.StatusOffline, LastActive: now}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := claudePeerEligible(test.session, now); got != test.want {
				t.Fatalf("eligible=%v want=%v", got, test.want)
			}
		})
	}
}

func TestNativeClaudeCapabilitiesMatchSocketContract(t *testing.T) {
	caps := surface.EffectiveCapabilities(&surface.Session{ID: "local-uuid", Surface: surface.KindClaude, Transport: "uds"}, surface.Capabilities{Send: true, Stream: true, Reply: true, Compact: true, Model: true, Interrupt: true, Steer: true})
	if caps.ReadOnly || !caps.Send || !caps.Reply || caps.Stream || caps.Compact || caps.Model || caps.Interrupt || caps.Steer {
		t.Fatalf("caps=%+v readOnly=%v", caps, caps.ReadOnly)
	}
}

func TestBridgedClaudeCapabilitiesUseRemoteControlContract(t *testing.T) {
	caps := surface.EffectiveCapabilities(&surface.Session{ID: "session_bridge", Surface: surface.KindClaude, Transport: "uds"}, surface.Capabilities{Send: true, Stream: true, Reply: true, Compact: true, Model: true, Interrupt: true, Steer: true})
	if caps.ReadOnly || !caps.Send || !caps.Reply || caps.Stream || !caps.Compact || !caps.Model || !caps.Interrupt || !caps.Steer {
		t.Fatalf("caps=%+v readOnly=%v", caps, caps.ReadOnly)
	}
}
