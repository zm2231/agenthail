package daemon

import (
	"encoding/json"
	"testing"
)

func TestAnAgentsCLIStartsRacingForTheLastSlotStartOneSession(t *testing.T) {
	f := newMCPCodexFixture(t)
	spawner, _, root := f.startParent(t)
	registerBusyChildren(t, f.registry, spawner, mcpChildrenLimit-1)
	results := make(chan scopeAnswer, 2)
	for _, key := range []string{"r1", "r2"} {
		go func() {
			payload, _ := json.Marshal(map[string]any{"surface": "codex", "cwd": root, "message": "race " + key})
			results <- askScope(t, f.daemon, ScopeRequest{SessionID: spawner, Action: "create", Create: payload}, "127.0.0.1:50000")
		}()
	}
	limited := 0
	for range 2 {
		if answer := <-results; answer.code == "children_limit" {
			limited++
		}
	}
	if starts := f.lifecycle.Daemon().Calls("thread/start"); limited != 1 || len(starts) != 2 {
		t.Fatalf("children_limit=%d thread/start=%d, want one refusal and one new thread beside the parent's", limited, len(starts))
	}
}

func TestAnAgentsCLIStartIsBoundedByItsCeiling(t *testing.T) {
	f := newMCPCodexFixture(t)
	spawner, _, root := f.startParent(t)
	payload, _ := json.Marshal(map[string]any{"surface": "codex", "cwd": root, "message": "help", "approvalPolicy": "never"})
	answer := askScope(t, f.daemon, ScopeRequest{SessionID: spawner, Action: "create", Create: payload}, "127.0.0.1:50000")
	var created struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if answer.status >= 300 || json.Unmarshal(answer.body, &created) != nil || created.Session.ID == "" {
		t.Fatalf("a start by an unbounded spawner: %d %s", answer.status, answer.body)
	}
	ceiling, err := f.registry.SessionCeiling(created.Session.ID)
	if bounds, _ := parseCeiling(ceiling); err != nil || len(bounds.Roots) != 1 || bounds.Roots[0] != root || bounds.Permission != "full" {
		t.Fatalf("the child's ceiling=%s err=%v", ceiling, err)
	}
	elsewhere, err := canonicalCatalogPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	payload, _ = json.Marshal(map[string]any{"surface": "codex", "cwd": elsewhere, "message": "help"})
	if refused := askScope(t, f.daemon, ScopeRequest{SessionID: created.Session.ID, Action: "create", Create: payload}, "127.0.0.1:50000"); refused.code != "exceeds_ceiling" {
		t.Fatalf("the child starting outside its folder: %+v", refused)
	}
	payload, _ = json.Marshal(map[string]any{"surface": "claude", "cwd": root, "message": "help", "configRoot": "/elsewhere/.claude"})
	if refused := askScope(t, f.daemon, ScopeRequest{SessionID: created.Session.ID, Action: "create", Create: payload}, "127.0.0.1:50000"); refused.code != "invalid" {
		t.Fatalf("the child choosing an unconfigured Claude root: %+v", refused)
	}
	if starts := f.lifecycle.Daemon().Calls("thread/start"); len(starts) != 2 {
		t.Fatalf("thread/start=%d, want the parent's and one child's", len(starts))
	}
}

func TestAnAgentsCLIStartCannotWidenItsChildsCeiling(t *testing.T) {
	f := newMCPCodexFixture(t)
	spawner, _, root := f.startParent(t)
	payload, _ := json.Marshal(map[string]any{"surface": "codex", "cwd": root, "message": "child", "approvalPolicy": "untrusted"})
	var child struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if answer := askScope(t, f.daemon, ScopeRequest{SessionID: spawner, Action: "create", Create: payload}, "127.0.0.1:50000"); json.Unmarshal(answer.body, &child) != nil || child.Session.ID == "" {
		t.Fatalf("child: %d %s", answer.status, answer.body)
	}
	payload, _ = json.Marshal(map[string]any{"surface": "codex", "cwd": root, "message": "grandchild", "ceiling": map[string]any{}, "task": true})
	var grandchild struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if answer := askScope(t, f.daemon, ScopeRequest{SessionID: child.Session.ID, Action: "create", Create: payload}, "127.0.0.1:50000"); json.Unmarshal(answer.body, &grandchild) != nil || grandchild.Session.ID == "" {
		t.Fatalf("grandchild: %d %s", answer.status, answer.body)
	}
	ceiling, err := f.registry.SessionCeiling(grandchild.Session.ID)
	if bounds, _ := parseCeiling(ceiling); err != nil || bounds.Permission != "ask" || len(bounds.Roots) != 1 || bounds.Roots[0] != root {
		t.Fatalf("a payload ceiling widened the grandchild: %s err=%v", ceiling, err)
	}
}
