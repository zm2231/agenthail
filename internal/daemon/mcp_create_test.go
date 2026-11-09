package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (f *mcpCodexFixture) startParent(t *testing.T) (string, string, string) {
	t.Helper()
	root, err := canonicalCatalogPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	code, created := f.action(t, map[string]any{"action": "session-create", "surface": "codex", "message": "lead the work", "cwd": root})
	session, _ := created["session"].(map[string]any)
	id, _ := session["id"].(string)
	if code >= 300 || id == "" {
		t.Fatalf("create status=%d", code)
	}
	starts := f.lifecycle.Daemon().Calls("thread/start")
	return id, codexBearer(starts[len(starts)-1]), root
}

func (f *mcpCodexFixture) create(t *testing.T, token string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	return callMCP(t, mcpSession(t, f.server, token), "agenthail_create", args)
}

func TestAChildStartsBoundedByItsSpawnerFromItsFirstCall(t *testing.T) {
	f := newMCPCodexFixture(t)
	_, parentToken, root := f.startParent(t)
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"surface": "codex", "cwd": sub, "message": "child work", "permissionMode": "on-request", "clientRequestId": "c1"}
	out, refused := f.create(t, parentToken, args)
	if refused {
		t.Fatalf("create: %v", out)
	}
	session, _ := out["session"].(map[string]any)
	childID, _ := session["id"].(string)
	starts := f.lifecycle.Daemon().Calls("thread/start")
	if childID == "" || len(starts) != 2 {
		t.Fatalf("child=%q thread/start=%d", childID, len(starts))
	}
	want := `{"permission":"edits","roots":["` + sub + `"]}`
	if stored, err := f.registry.SessionCeiling(childID); err != nil || string(stored) != want {
		t.Fatalf("stored ceiling=%s err=%v, want %s", stored, err, want)
	}
	childToken := codexBearer(starts[1])
	whoami, refused := f.whoami(t, childToken)
	if ceiling, _ := json.Marshal(whoami["ceiling"]); refused || string(ceiling) != want {
		t.Fatalf("the child's first credential has ceiling %s, want %s", ceiling, want)
	}
	if out, _ := f.create(t, childToken, map[string]any{"surface": "codex", "cwd": sub, "message": "more", "permissionMode": "never", "clientRequestId": "g1"}); out["code"] != "exceeds_ceiling" {
		t.Fatalf("a grandchild above the child's mode: %v", out)
	}
	if out, _ := f.create(t, childToken, map[string]any{"surface": "codex", "cwd": root, "message": "more", "clientRequestId": "g2"}); out["code"] != "exceeds_ceiling" {
		t.Fatalf("a grandchild outside the child's folder: %v", out)
	}
	ceilingAudits := map[string]string{}
	for _, entry := range mcpAudits(t, f.registry) {
		if entry.Result == "exceeds_ceiling" {
			ceilingAudits[entry.Message] = entry.SessionID
		}
	}
	if !strings.HasPrefix(ceilingAudits["agenthail_create rule=ceiling_permission"], "new:codex:") || ceilingAudits["agenthail_create rule=ceiling_roots"] != "new:codex:"+root {
		t.Fatalf("ceiling refusal audits=%v, want each with its new-session target and rule", ceilingAudits)
	}
	if out, refused := f.create(t, parentToken, args); refused {
		t.Fatalf("replay: %v", out)
	}
	if starts := f.lifecycle.Daemon().Calls("thread/start"); len(starts) != 2 {
		t.Fatalf("a repeated clientRequestId started %d threads, want the one", len(starts))
	}
}

func TestAPersonStartedSessionIsUnboundedAndStoresNoCeiling(t *testing.T) {
	f := newMCPCodexFixture(t)
	id, token, _ := f.startParent(t)
	whoami, refused := f.whoami(t, token)
	if ceiling, _ := json.Marshal(whoami["ceiling"]); refused || whoami["sessionId"] != id || string(ceiling) != `{}` {
		t.Fatalf("whoami=%v refused=%v", whoami, refused)
	}
	var ceilings, pending int
	if err := f.registry.WriteDB().QueryRow(`SELECT (SELECT COUNT(*) FROM session_ceilings),(SELECT COUNT(*) FROM pending_ceilings)`).Scan(&ceilings, &pending); err != nil || ceilings != 0 || pending != 0 {
		t.Fatalf("session_ceilings=%d pending_ceilings=%d err=%v, want none for a person's session", ceilings, pending, err)
	}
}

func registerChain(t *testing.T, r *registry.Registry, ids ...string) {
	t.Helper()
	for index, id := range ids {
		if err := r.RegisterSession(surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle}); err != nil {
			t.Fatal(err)
		}
		creator := ""
		if index > 0 {
			creator = ids[index-1]
		}
		if err := r.RecordSessionOrigin(registry.OriginOf(id, creator)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSessionsNestAtMostThreeDeep(t *testing.T) {
	f := newMCPCodexFixture(t)
	registerChain(t, f.registry, "root", "one", "two", "three")
	token := issueMCPToken(t, f.daemon, surface.Session{ID: "three"})
	if out, _ := f.create(t, token, map[string]any{"surface": "codex", "cwd": t.TempDir(), "message": "deeper", "clientRequestId": "d1"}); out["code"] != "depth_limit" {
		t.Fatalf("a fourth level: %v", out)
	}
	if appServer := f.lifecycle.Daemon(); appServer != nil && len(appServer.Calls("thread/start")) != 0 {
		t.Fatal("a refused create started a thread")
	}
}

func registerBusyChildren(t *testing.T, r *registry.Registry, spawner string, count int) {
	t.Helper()
	for index := range count {
		child := fmt.Sprintf("busy-%d", index)
		if err := r.RegisterSession(surface.Session{ID: child, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusBusy}); err != nil {
			t.Fatal(err)
		}
		if err := r.RecordSessionOrigin(registry.OriginOf(child, spawner)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTwoCreatesRacingForTheLastSlotStartOneSession(t *testing.T) {
	f := newMCPCodexFixture(t)
	spawner, token, root := f.startParent(t)
	registerBusyChildren(t, f.registry, spawner, mcpChildrenLimit-1)
	results := make(chan map[string]any, 2)
	for _, key := range []string{"r1", "r2"} {
		go func() {
			out, _ := callMCP(t, mcpSession(t, f.server, token), "agenthail_create", map[string]any{"surface": "codex", "cwd": root, "message": "race " + key, "clientRequestId": key})
			results <- out
		}()
	}
	limited := 0
	for range 2 {
		if out := <-results; out["code"] == "children_limit" {
			limited++
		}
	}
	if starts := f.lifecycle.Daemon().Calls("thread/start"); limited != 1 || len(starts) != 2 {
		t.Fatalf("children_limit=%d thread/start=%d, want one refusal and one new thread beside the parent's", limited, len(starts))
	}
}

func TestASpawnerRunsAtMostEightChildrenAtOnce(t *testing.T) {
	f := newMCPCodexFixture(t)
	registerChain(t, f.registry, "spawner")
	for index := range mcpChildrenLimit {
		child := fmt.Sprintf("busy-%d", index)
		if err := f.registry.RegisterSession(surface.Session{ID: child, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusBusy}); err != nil {
			t.Fatal(err)
		}
		if err := f.registry.RecordSessionOrigin(registry.OriginOf(child, "spawner")); err != nil {
			t.Fatal(err)
		}
	}
	token := issueMCPToken(t, f.daemon, surface.Session{ID: "spawner"})
	if out, _ := f.create(t, token, map[string]any{"surface": "codex", "cwd": t.TempDir(), "message": "ninth", "clientRequestId": "n1"}); out["code"] != "children_limit" {
		t.Fatalf("a ninth running child: %v", out)
	}
}
