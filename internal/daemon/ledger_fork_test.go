package daemon

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func registerFork(t *testing.T, r *registry.Registry, id, parent, cwd string) surface.Session {
	t.Helper()
	s := surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: cwd, Status: surface.StatusIdle, ForkedFrom: parent}
	if err := r.RegisterSession(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func backdateGrant(t *testing.T, r *registry.Registry, key, sessionID string, at time.Time) {
	t.Helper()
	if _, err := r.WriteDB().Exec(`UPDATE ledger_grants SET since_ms=? WHERE project_key=? AND session_id=?`, at.UnixMilli(), key, sessionID); err != nil {
		t.Fatal(err)
	}
}

func TestAForkOfAnOptedInSessionInheritsTheGrantFromItsCreation(t *testing.T) {
	d, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-fork", "fork-key")
	outside := t.TempDir()
	registerFork(t, r, "parent", "", outside)
	if err := p.store.GrantSession(p.key, "parent"); err != nil {
		t.Fatal(err)
	}
	backdateGrant(t, r, p.key, "parent", time.Now().Add(-2*time.Hour))
	fork := registerFork(t, r, "fork", "parent", outside)
	grandchild := registerFork(t, r, "grandchild", "fork", outside)

	for _, id := range []string{"fork", "grandchild"} {
		resp := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: id, PromptID: "p-" + id, Prompt: "words to " + id})
		if !resp.Captured || resp.ProjectKey != p.key {
			t.Fatalf("%s did not inherit the grant: %+v", id, resp)
		}
	}
	journalSource(d, fork, nil).appendSeedItems([]surface.TimelineItem{typedPrompt("old-fork", "old-fork", "said before the fork existed", time.Now().Add(-time.Hour))})
	journalSource(d, grandchild, nil).appendSeedItems([]surface.TimelineItem{typedPrompt("old-gc", "old-gc", "said before the fork existed", time.Now().Add(-time.Hour))})
	events := p.events(t)
	if len(events) != 2 || events[0].Ref != ledger.PromptRef("p-fork") || events[1].Ref != ledger.PromptRef("p-grandchild") {
		t.Fatalf("want only words after each fork's creation, got %+v", events)
	}

	if err := p.store.RevokeSession(p.key, "parent"); err != nil {
		t.Fatal(err)
	}
	if ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "fork", PromptID: "revoked", Prompt: "after revoke"}).Captured {
		t.Fatal("a fork kept a grant its ancestor lost")
	}
	if ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "parent", PromptID: "parent", Prompt: "parent itself"}).Captured {
		t.Fatal("the revoked parent was captured")
	}
}

func TestAForkOfTheCoordinatorIsCapturedInTheUnsetProject(t *testing.T) {
	_, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-cfork", "cfork-key")
	p.session(t, r, "coord")
	makeCoordinator(t, r, "proj-cfork", p, "coord")
	registerFork(t, r, "coord-fork", "coord", p.dir)
	p.session(t, r, "bystander")

	if resp := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "coord-fork", PromptID: "cf", Prompt: "to the fork"}); !resp.Captured || resp.ProjectKey != p.key {
		t.Fatalf("a fork of the coordinator was not captured: %+v", resp)
	}
	if ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "bystander", PromptID: "b", Prompt: "to a bystander"}).Captured {
		t.Fatal("a session outside the lineage was captured in the unset project")
	}
}

func TestAGrantGivenToTheForkItselfKeepsItsOwnSince(t *testing.T) {
	_, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-own", "own-key")
	outside := t.TempDir()
	registerFork(t, r, "root", "", outside)
	registerFork(t, r, "own", "root", outside)
	if ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "own", PromptID: "none", Prompt: "nobody opted in"}).Captured {
		t.Fatal("a fork of a session without a grant was captured")
	}
	if err := p.store.GrantSession(p.key, "own"); err != nil {
		t.Fatal(err)
	}
	if resp := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "own", PromptID: "own", Prompt: "opted in directly"}); !resp.Captured {
		t.Fatalf("a directly granted fork was not captured: %+v", resp)
	}
}
