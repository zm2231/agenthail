package surfaces

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func writeCodexRollout(t *testing.T, dir, name, firstLine string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(firstLine+"\n"+`{"type":"turn_context","payload":{}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadCodexRolloutSubagentParsesThreadSpawnAtEachDepth(t *testing.T) {
	dir := t.TempDir()
	child := writeCodexRollout(t, dir, "child.jsonl", `{"type":"session_meta","payload":{"id":"child","session_id":"root","agent_nickname":"Ada","agent_role":null,"source":{"subagent":{"thread_spawn":{"parent_thread_id":"root","depth":1,"agent_path":null,"agent_nickname":"Ada","agent_role":null}}}}}`)
	grandchild := writeCodexRollout(t, dir, "grandchild.jsonl", `{"type":"session_meta","payload":{"id":"grandchild","session_id":"root","source":{"subagent":{"thread_spawn":{"parent_thread_id":"child","depth":2,"agent_nickname":"Bo","agent_role":"reviewer"}}}}}`)
	top := writeCodexRollout(t, dir, "top.jsonl", `{"type":"session_meta","payload":{"id":"root","session_id":"root","source":"vscode"}}`)

	got, err := readCodexRolloutSubagent(child)
	if err != nil {
		t.Fatal(err)
	}
	if want := (&surface.Subagent{ParentID: "root", RootID: "root", Depth: 1, Nickname: "Ada"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("depth 1 = %+v, want %+v", got, want)
	}
	got, err = readCodexRolloutSubagent(grandchild)
	if err != nil {
		t.Fatal(err)
	}
	if want := (&surface.Subagent{ParentID: "child", RootID: "root", Depth: 2, Nickname: "Bo", Role: "reviewer"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("depth 2 = %+v, want %+v", got, want)
	}
	got, err = readCodexRolloutSubagent(top)
	if err != nil || got != nil {
		t.Fatalf("top-level rollout = %+v, %v; want nil, nil", got, err)
	}
}

func TestReadCodexRolloutSubagentRejectsAMissingRoot(t *testing.T) {
	path := writeCodexRollout(t, t.TempDir(), "child.jsonl", `{"type":"session_meta","payload":{"id":"child","session_id":"child","source":{"subagent":{"thread_spawn":{"parent_thread_id":"root","depth":1}}}}}`)
	if _, err := readCodexRolloutSubagent(path); err == nil {
		t.Fatal("expected a session_meta that names itself as root to be rejected")
	}
	other := writeCodexRollout(t, t.TempDir(), "other.jsonl", `{"type":"turn_context","payload":{}}`)
	if _, err := readCodexRolloutSubagent(other); err == nil {
		t.Fatal("expected a rollout without session_meta to be rejected")
	}
}

func TestCodexSessionReadsSubagentIdentityFromThread(t *testing.T) {
	dir := t.TempDir()
	grandchildPath := writeCodexRollout(t, dir, "grandchild.jsonl", `{"type":"session_meta","payload":{"id":"grandchild","session_id":"root","source":{"subagent":{"thread_spawn":{"parent_thread_id":"child","depth":2,"agent_nickname":"Bo"}}}}}`)
	child := codexSession(map[string]any{
		"id":             "child",
		"parentThreadId": "root",
		"agentNickname":  "Ada",
		"source":         map[string]any{"subAgent": map[string]any{"thread_spawn": map[string]any{"parent_thread_id": "root", "depth": float64(1), "agent_nickname": "Ada"}}},
	}, false, true)
	if want := (&surface.Subagent{ParentID: "root", RootID: "root", Depth: 1, Nickname: "Ada"}); !reflect.DeepEqual(child.Subagent, want) {
		t.Fatalf("child subagent = %+v, want %+v", child.Subagent, want)
	}
	if child.Source != "subAgent" {
		t.Fatalf("child source = %q", child.Source)
	}
	grandchild := codexSession(map[string]any{
		"id":             "grandchild",
		"parentThreadId": "child",
		"agentNickname":  "Bo",
		"agentRole":      "reviewer",
		"path":           grandchildPath,
		"source":         map[string]any{"subAgent": map[string]any{"thread_spawn": map[string]any{"parent_thread_id": "child", "depth": float64(2)}}},
	}, false, true)
	if want := (&surface.Subagent{ParentID: "child", RootID: "root", Depth: 2, Nickname: "Bo", Role: "reviewer"}); !reflect.DeepEqual(grandchild.Subagent, want) {
		t.Fatalf("grandchild subagent = %+v, want %+v", grandchild.Subagent, want)
	}
	unreadable := codexSession(map[string]any{
		"id":             "grandchild-2",
		"parentThreadId": "child",
		"path":           filepath.Join(dir, "missing.jsonl"),
		"source":         map[string]any{"subAgent": map[string]any{"thread_spawn": map[string]any{"parent_thread_id": "child", "depth": float64(2)}}},
	}, false, true)
	if unreadable.Subagent == nil || unreadable.Subagent.RootID != "" || unreadable.Subagent.ParentID != "child" {
		t.Fatalf("a nested subagent without a readable rollout keeps its parent and leaves the root unknown: %+v", unreadable.Subagent)
	}
	top := codexSession(map[string]any{"id": "root", "source": "vscode"}, false, true)
	if top.Subagent != nil {
		t.Fatalf("top-level thread has subagent identity %+v", top.Subagent)
	}
}
