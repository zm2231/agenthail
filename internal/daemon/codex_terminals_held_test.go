package daemon

import (
	"context"
	"os"
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

func appendRollout(t *testing.T, path string, lines ...string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
}

func TestARelayStaysHeldWhileAnOlderCodexsTranscriptShowsItsShellRunning(t *testing.T) {
	const threadID = "019f004a-0000-7000-8000-0000000b0001"
	home := codextest.Home(t)
	fake := codextest.Serve(t, home, func(method string, _ map[string]any) map[string]any {
		switch method {
		case "thread/backgroundTerminals/list":
			return map[string]any{"error": map[string]any{"code": -32600, "message": "Invalid request: unknown variant `thread/backgroundTerminals/list`"}}
		case "thread/items/list":
			return map[string]any{"data": []any{
				map[string]any{"type": "userMessage", "id": "user-1", "content": []any{map[string]any{"type": "text", "text": "build it"}}},
				map[string]any{"type": "agentMessage", "id": "reply-1", "text": "built"},
			}}
		}
		return nil
	})
	fake.AddThread(codextest.Thread{ID: threadID, Originator: "agenthail", Cwd: t.TempDir(), Turns: []codextest.Turn{{ID: "turn-0", Status: "completed", User: "set up", Assistant: "ready"}, {ID: "turn-1", Status: "inProgress", User: "build it"}}})
	fake.Load(threadID)
	rollout := fake.RolloutPath(threadID)

	codex := surfaces.NewCodex(unreachableDesktop)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		codex.NotifyCatalog(ctx, func() {})
	}()
	t.Cleanup(func() { cancel(); <-done })
	testkit.Eventually(t, "the watch to ask for the thread's terminals", 5*time.Second, func() bool {
		return len(fake.Calls("thread/backgroundTerminals/list")) > 0
	})
	time.Sleep(200 * time.Millisecond)

	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	sessions, err := codex.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var from surface.Session
	for _, session := range sessions {
		if session.ID == threadID {
			from = session
		}
	}
	to := surface.Session{ID: "to", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
	for _, session := range []surface.Session{from, to} {
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	d := newTestDaemon(t, r, []surface.Surface{codex})
	if err := r.PrepareRelaySource(ctx, codex, &from, registry.HandoffTurnFinished); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRelayRoute(from.ID, to.ID, ".*", false, registry.HandoffTurnFinished); err != nil {
		t.Fatal(err)
	}
	d.syncRelays(ctx, codex, &from)

	appendRollout(t, rollout,
		`{"timestamp":"2026-10-26T10:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"timestamp":"2026-10-26T10:00:01Z","type":"response_item","payload":{"type":"custom_tool_call","name":"exec","call_id":"call-build","input":"text((await tools.exec_command({cmd:\"make build\", yield_time_ms: 1000})).output)"}}`,
		`{"timestamp":"2026-10-26T10:00:02Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call-build","output":[{"type":"input_text","text":"Script running with cell ID 3\nWall time 1.0 seconds\nOutput:\ncompiling\n"}]}}`,
		`{"timestamp":"2026-10-26T10:00:03Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}`,
	)
	fake.CompleteTurn(threadID, "built")
	d.syncRelays(ctx, codex, &from)
	d.syncRelays(ctx, codex, &from)
	if count := r.QueueCount(to.ID); count != 0 {
		t.Fatalf("relayed %d while the transcript shows the shell running", count)
	}
	listed, err := codex.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range listed {
		if background := session.BackgroundTasks; session.ID == threadID && (!surface.BackgroundRunning(background) || background.Source != surface.BackgroundSourceTranscript) {
			t.Fatalf("listed background=%+v, want the transcript's running command", background)
		}
	}

	appendRollout(t, rollout, `{"timestamp":"2026-10-26T10:01:00Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-1","item":{"type":"CommandExecution","id":"exec-build","command":["/bin/zsh","-lc","make build"],"aggregated_output":"ok\n","exit_code":0,"status":"completed"}}}`)
	d.syncRelays(ctx, codex, &from)
	d.syncRelays(ctx, codex, &from)
	if count := r.QueueCount(to.ID); count != 1 {
		t.Fatalf("relayed %d once the shell finished", count)
	}
}
