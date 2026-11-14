package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/tidwall/gjson"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

// backgroundShellModel starts command as a terminal that outlives the turn,
// then ends the turn once Codex reports the call's output.
type backgroundShellModel struct{ command string }

func (m backgroundShellModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(event map[string]any) {
		data, _ := json.Marshal(event)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data)
	}
	id := fmt.Sprintf("resp_%d", time.Now().UnixNano())
	send(map[string]any{"type": "response.created", "response": map[string]any{"id": id}})
	if gjson.GetBytes(body, "input.@reverse.0.type").String() == "function_call_output" {
		send(map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "message", "role": "assistant", "id": "msg_" + id, "content": []any{map[string]any{"type": "output_text", "text": "started"}}}})
	} else {
		arguments, _ := json.Marshal(map[string]any{"cmd": m.command, "yield_time_ms": 250})
		send(map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "function_call", "id": "fc_" + id, "call_id": "call_" + id, "name": "exec_command", "arguments": string(arguments)}})
	}
	send(map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "usage": map[string]any{"input_tokens": 10, "output_tokens": 1, "total_tokens": 11, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens_details": map[string]any{"reasoning_tokens": 0}}}})
}

func TestARealCodexThreadListsItsBackgroundTerminalUntilItExits(t *testing.T) {
	realManagedCodexWith(t, backgroundShellModel{command: "sleep 6"})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	codex := inDaemon(NewCodex("1"))
	done := make(chan struct{})
	go func() {
		defer close(done)
		codex.NotifyCatalog(ctx, func() {})
	}()
	t.Cleanup(func() { cancel(); <-done })
	owner, err := dialManagedCodex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Close() })
	started, err := owner.Request(ctx, "thread/start", map[string]any{"cwd": t.TempDir(), "sandbox": "danger-full-access", "approvalPolicy": "never"}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	threadID := gjson.Get(codexJSON(started), "result.thread.id").String()
	realTurn(t, owner, threadID, "start a sleep in the background")

	testkit.Eventually(t, "the row to list the running terminal", 10*time.Second, func() bool {
		background := listedCodexSession(t, codex, threadID).BackgroundTasks
		return surface.BackgroundRunning(background) && background.Source == surface.BackgroundSourceRuntime &&
			len(background.Tasks) == 1 && background.Tasks[0].Description == "sleep 6" && background.Tasks[0].Kind == surface.BackgroundShell
	})
	testkit.Eventually(t, "the row to drop the terminal once it exits", 20*time.Second, func() bool {
		return listedCodexSession(t, codex, threadID).BackgroundTasks == nil
	})
}
