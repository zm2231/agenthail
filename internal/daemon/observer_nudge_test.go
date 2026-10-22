package daemon

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// statusFileAgent reports each session's status from a file, as Claude's
// session records do, so the daemon's status pass can see a turn end.
type statusFileAgent struct {
	*scriptedAgent
	dir string
}

func (a *statusFileAgent) LocalStatusFiles(session surface.Session) []string {
	return []string{filepath.Join(a.dir, session.ID)}
}

func (a *statusFileAgent) LocalStatus(_ context.Context, session surface.Session) (surface.Session, error) {
	raw, err := os.ReadFile(filepath.Join(a.dir, session.ID))
	if err != nil {
		return session, err
	}
	session.Status = surface.SessionStatus(strings.TrimSpace(string(raw)))
	return session, nil
}

func (a *statusFileAgent) write(t *testing.T, id string, status surface.SessionStatus) {
	t.Helper()
	path := filepath.Join(a.dir, id)
	if err := os.WriteFile(path, []byte(status), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestAQueuedMessageIsDeliveredWhenTheStatusPassSeesTheTargetGoIdle(t *testing.T) {
	agent := &statusFileAgent{scriptedAgent: newScriptedAgent("sender", "target"), dir: t.TempDir()}
	agent.setStatus("target", surface.StatusBusy)
	agent.write(t, "sender", surface.StatusIdle)
	agent.write(t, "target", surface.StatusBusy)
	t.Setenv("HOME", t.TempDir())
	store, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	for id := range agent.sessions {
		if err := store.RegisterSession(agent.session(id)); err != nil {
			t.Fatal(err)
		}
	}
	d := newTestDaemon(t, store, []surface.Surface{agent})
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: e2eToken}))
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = d.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
		server.Close()
		store.Close()
	})
	h := &runningDaemon{t: t, agent: agent.scriptedAgent, server: server, store: store, daemon: d}
	receipt := h.receipt(map[string]any{"action": "send", "sessionId": "target", "sourceSessionId": "sender", "message": "after this turn"})
	if receipt["evidence"] != string(surface.EvidenceQueued) {
		t.Fatalf("receipt=%v", receipt)
	}
	// Let the observer's first pass and the status pass settle on busy.
	time.Sleep(1500 * time.Millisecond)
	if len(agent.deliveredTo("target")) != 0 {
		t.Fatal("busy target received the queued message early")
	}
	idle := time.Now()
	agent.setStatus("target", surface.StatusIdle)
	agent.write(t, "target", surface.StatusIdle)
	eventuallyWithin(t, "queued delivery after the target went idle", defaultPollInterval, func() bool { return len(agent.deliveredTo("target")) > 0 })
	latency := time.Since(idle)
	t.Logf("idle to delivery: %s", latency)
	if latency > 2500*time.Millisecond {
		t.Fatalf("delivery waited %s for the observer tick", latency)
	}
}
