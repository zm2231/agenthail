package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tidwall/gjson"
	"github.com/zm2231/agenthail/internal/surface"
)

// commandModel asks to run one shell command on the turn's first request and
// replies once the command's result comes back.
type commandModel struct {
	command string
	mu      sync.Mutex
	turns   int
}

func (m *commandModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	m.mu.Lock()
	m.turns++
	id := fmt.Sprintf("resp_%d", m.turns)
	m.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(event map[string]any) {
		data, _ := json.Marshal(event)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data)
	}
	send(map[string]any{"type": "response.created", "response": map[string]any{"id": id}})
	if strings.Contains(string(body), "function_call_output") {
		send(map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "message", "role": "assistant", "id": "msg_" + id, "content": []any{map[string]any{"type": "output_text", "text": "done"}}}})
	} else {
		arguments, _ := json.Marshal(map[string]any{"cmd": m.command, "sandbox_permissions": "require_escalated", "justification": "write the marker"})
		send(map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "function_call", "id": "fc_" + id, "call_id": "call_1", "name": "exec_command", "arguments": string(arguments)}})
	}
	send(map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "usage": map[string]any{"input_tokens": 10, "output_tokens": 10, "total_tokens": 20, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens_details": map[string]any{"reasoning_tokens": 0}}}})
}

func TestACodexApprovalAnotherClientLeftOpenIsAnsweredThroughAgenthail(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	realManagedCodexWith(t, &commandModel{command: "touch " + marker})
	ctx := context.Background()
	owner, err := dialManagedCodex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	started, err := owner.Request(ctx, "thread/start", map[string]any{"cwd": t.TempDir(), "approvalPolicy": "untrusted", "sandbox": "read-only"}, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	threadID := gjson.Get(codexJSON(started), "result.thread.id").String()
	if _, err := owner.Request(ctx, "turn/start", map[string]any{"threadId": threadID, "input": []any{map[string]any{"type": "text", "text": "write the marker"}}}, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	status := func() string {
		response, err := owner.Request(ctx, "thread/read", map[string]any{"threadId": threadID, "includeTurns": false}, 5*time.Second)
		if err != nil {
			return ""
		}
		return gjson.Get(codexJSON(response), "result.thread.status").Raw
	}
	for deadline := time.Now().Add(30 * time.Second); !strings.Contains(status(), "waitingOnApproval"); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the thread never waited on an approval: %s", status())
		}
	}
	_ = owner.Close()

	codex := isolatedManagedRuntime(t)
	session := listedCodexSession(t, codex, threadID)
	observation, err := codex.Observe(ctx, &session)
	if err != nil {
		t.Fatal(err)
	}
	want := threadID + ":call_1"
	if waiting := observation.WaitingOn; waiting == nil || waiting.ID != want || !waiting.Answerable || waiting.Kind != surface.WaitingApproval {
		t.Fatalf("observed wait %+v, want an answerable approval %s", waiting, want)
	}
	if listed := listedCodexSession(t, codex, threadID); !surface.SameWaiting(listed.WaitingOn, observation.WaitingOn) {
		t.Fatalf("the listing waits on %+v, the observation on %+v", listed.WaitingOn, observation.WaitingOn)
	}
	state, err := codex.Requests(ctx, &session)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Requests) != 1 || state.Requests[0].ID != want || !strings.Contains(state.Requests[0].Title, marker) {
		t.Fatalf("requests %+v", state.Requests)
	}
	var actions []string
	for _, action := range state.Requests[0].Actions() {
		actions = append(actions, action.ID)
	}
	if !reflect.DeepEqual(actions, []string{"allow", "always", "deny"}) {
		t.Fatalf("actions %v: Codex offered accept, an execpolicy amendment and cancel", actions)
	}
	outcome, err := codex.Respond(ctx, &session, want, surface.Decision{Behavior: "deny"})
	if err != nil || outcome != "denied" {
		t.Fatalf("respond: %q %v", outcome, err)
	}
	if _, err := codex.Respond(ctx, &session, want, surface.Decision{Behavior: "deny"}); !errors.Is(err, surface.ErrRequestStale) {
		t.Fatalf("a second answer: %v, want stale", err)
	}
	reader, err := dialManagedCodex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	owner = reader
	for deadline := time.Now().Add(30 * time.Second); strings.Contains(status(), "active"); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the turn did not end after the denial: %s", status())
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the denied command ran: %v", err)
	}
}

func listedCodexSession(t *testing.T, codex *Codex, threadID string) surface.Session {
	t.Helper()
	sessions, err := codex.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		if session.ID == threadID {
			return session
		}
	}
	t.Fatalf("thread %s is not listed", threadID)
	return surface.Session{}
}
