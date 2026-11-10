package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/testkit"
)

// codexMoveNode is a daemon whose Codex adapter runs on its own real Codex
// app-server and home.
type codexMoveNode struct {
	*federationNode
	real  *codextest.Real
	codex *surfaces.Codex
	cwd   string
}

func newCodexMoveNode(t *testing.T) *codexMoveNode {
	t.Helper()
	allowLoopbackHosts(t)
	real := codextest.StartReal(t, &codextest.MockModel{})
	codex := surfaces.NewCodexIn(surfaces.CodexHome{Codex: real.CodexHome, State: filepath.Join(real.Home, ".agenthail")}, "")
	reg, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	d := newTestDaemon(t, reg, []surface.Surface{codex})
	node := &federationNode{daemon: d, reg: reg, gate: &actionGate{}}
	node.handler = d.dashboardHandler(&dashboardServer{token: dashboardTestToken})
	cwd := filepath.Join(real.Home, "work", "widget")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	return &codexMoveNode{federationNode: node, real: real, codex: codex, cwd: cwd}
}

func (n *codexMoveNode) rollouts(t *testing.T, threadID string) []string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(n.real.CodexHome, "sessions", "*", "*", "*", "*"+threadID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func (n *codexMoveNode) idle(t *testing.T, threadID, what string) *surface.Session {
	t.Helper()
	var session *surface.Session
	testkit.Eventually(t, what, 60*time.Second, func() bool {
		resolved, err := n.codex.Resolve(context.Background(), threadID)
		if err != nil || resolved.Status != surface.StatusIdle {
			return false
		}
		session = resolved
		return true
	})
	return session
}

func TestAnIdleCodexThreadMovesToAPairedHostAndRunsThere(t *testing.T) {
	source, target := newCodexMoveNode(t), newCodexMoveNode(t)
	elsewhere := t.TempDir()
	t.Setenv("HOME", elsewhere)
	t.Setenv("CODEX_HOME", filepath.Join(elsewhere, ".codex"))
	checkout := checkoutsIn(t, source.cwd, target.real.Home, target.federationNode)
	target.serve(t, nil)
	source.start(t, testRoots(target.server))
	record := source.pair(t, target.federationNode, "server")
	testkit.Eventually(t, "the target connected", 10*time.Second, func() bool { return source.daemon.hostConnected(record.ID) })

	ctx := context.Background()
	started, _, err := source.codex.StartSession(ctx, surface.SessionStartOptions{Message: "remember kestrel", Cwd: source.cwd})
	if err != nil {
		t.Fatal(err)
	}
	session := source.idle(t, started.ID, "the first turn to end")
	if err := source.reg.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
	if err := source.reg.SetAlias("builder", session.ID); err != nil {
		t.Fatal(err)
	}
	pinnedAt, err := source.reg.PinSession(session.ID, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	before := source.rollouts(t, session.ID)
	if len(before) != 1 {
		t.Fatalf("source rollouts=%v", before)
	}
	written, err := os.ReadFile(before[0])
	if err != nil {
		t.Fatal(err)
	}

	result, err := source.daemon.moveSession(ctx, sessionMoveRequest{SessionID: session.ID, Host: "server"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Move.State != registry.MoveDone || result.Alias != "builder" {
		t.Fatalf("result=%+v", result)
	}
	if owner, _ := source.reg.SessionHost(session.ID); owner != record.ID {
		t.Fatalf("source owner=%q, want %q", owner, record.ID)
	}
	if left := source.rollouts(t, session.ID); len(left) != 0 {
		t.Fatalf("the source still reads the thread at %v", left)
	}

	moved, err := target.reg.Session(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(checkout)
	if err != nil {
		t.Fatal(err)
	}
	if owner, _ := target.reg.SessionHost(session.ID); owner != "" || moved.Surface != surface.KindCodex || moved.Cwd != resolved {
		t.Fatalf("target row=%+v owner=%q, want a local Codex thread in %s", moved, owner, resolved)
	}
	if alias, _ := target.reg.LookupAlias("builder"); alias != session.ID {
		t.Fatalf("target alias=%q", alias)
	}
	if kept, err := target.reg.PinnedSessions(); err != nil || len(kept) != 1 || kept[0].Session.ID != session.ID || !kept[0].PinnedAt.Equal(pinnedAt) {
		t.Fatalf("the target does not keep the pinned session: %+v %v", kept, err)
	}
	placed := target.rollouts(t, session.ID)
	if len(placed) != 1 {
		t.Fatalf("target rollouts=%v", placed)
	}
	if data, err := os.ReadFile(placed[0]); err != nil || !strings.HasPrefix(string(data), string(written)) {
		t.Fatalf("the target's rollout does not continue the source's: %v", err)
	}

	if sent, err := target.codex.Send(ctx, moved, "on the target"); err != nil || !sent.Accepted {
		t.Fatalf("a send on the target: %+v %v", sent, err)
	}
	target.idle(t, session.ID, "the target's turn to end")
	if data, err := os.ReadFile(placed[0]); err != nil || !strings.Contains(string(data), "on the target") {
		t.Fatalf("the target's turn is not in its rollout: %v", err)
	}
	if entries, err := os.ReadDir(elsewhere); err != nil || len(entries) != 0 {
		t.Fatalf("a daemon wrote into the process's current home: %v %v", entries, err)
	}
}
