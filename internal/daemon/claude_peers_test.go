package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestRecentPeerRegistrationIncludesLatestPageAndReadOnly(t *testing.T) {
	reg, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	codex := &daemonSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{
		"recent":    {ID: "recent", Surface: surface.KindCodex, Transport: "managed", Status: surface.StatusIdle, LastActive: time.Now()},
		"last-page": {ID: "last-page", Surface: surface.KindCodex, Transport: "readOnly", Status: surface.StatusIdle, LastActive: time.Now().Add(-48 * time.Hour)},
		"offline":   {ID: "offline", Surface: surface.KindCodex, Status: surface.StatusOffline},
	}}
	notion := &daemonSurface{kind: surface.KindNotion, sessions: map[string]surface.Session{"notes": {ID: "notes", Surface: surface.KindNotion, Status: surface.StatusIdle}}}
	claude := &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{"native": {ID: "native", Surface: surface.KindClaude}}}
	d := New(reg, []surface.Surface{codex, notion, claude})
	seen := map[string]int{}
	ensure := func(ctx context.Context, id string) error {
		if _, err := reg.Session(id); err != nil {
			t.Fatalf("ensure before persistence: %v", err)
		}
		seen[id]++
		return nil
	}
	d.registerRecentClaudePeers(context.Background(), ensure)
	if len(seen) != 1 || seen["recent"] != 1 {
		t.Fatalf("registered=%v", seen)
	}
	if claude.listCalls.Load() != 0 {
		t.Fatal("native Claude registry must not be proxied")
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
