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
	if len(seen) != 3 || seen["recent"] != 1 || seen["last-page"] != 1 || seen["notes"] != 1 {
		t.Fatalf("registered=%v", seen)
	}
	if claude.listCalls.Load() != 0 {
		t.Fatal("native Claude registry must not be proxied")
	}
}

func TestNativeClaudeCapabilitiesMatchSocketContract(t *testing.T) {
	caps, readOnly, _ := dashboardCapabilities(surface.Session{ID: "local-uuid", Surface: surface.KindClaude, Transport: "uds"}, surface.Capabilities{Send: true, Stream: true, Reply: true, Compact: true, Model: true, Interrupt: true, Steer: true})
	if readOnly || !caps.Send || !caps.Reply || caps.Stream || caps.Compact || caps.Model || caps.Interrupt || caps.Steer {
		t.Fatalf("caps=%+v readOnly=%v", caps, readOnly)
	}
}
