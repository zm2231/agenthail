package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func TestAHostSessionReportsTheBackgroundTasksItsHostLists(t *testing.T) {
	c, fixture := hostedClaude(t)
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	shell := uuid.NewString()
	if _, err := client.Send(ctx, claudehost.SendRequest{SessionID: id, UUID: shell, Text: "BACKGROUND 3000"}); err != nil {
		t.Fatal(err)
	}
	watcher := uuid.NewString()
	if _, err := client.Send(ctx, claudehost.SendRequest{SessionID: id, UUID: watcher, Text: "AMBIENT 3000"}); err != nil {
		t.Fatal(err)
	}
	var sess *surface.Session
	claudehosttest.WaitFor(t, "both tasks to be listed", func() bool {
		sess = listedSession(t, c, id)
		return sess.BackgroundTasks != nil && len(sess.BackgroundTasks.Tasks) == 2
	})
	background := sess.BackgroundTasks
	shellTask, watcherTask := background.Tasks[0], background.Tasks[1]
	if !background.Running || background.Source != surface.BackgroundSourceHost || background.Since.IsZero() ||
		shellTask.ID != "task-"+shell || shellTask.Kind != surface.BackgroundShell || shellTask.Description != "background shell" || shellTask.Ambient ||
		watcherTask.ID != "ambient-"+watcher || watcherTask.Kind != surface.BackgroundMonitor || !watcherTask.Ambient {
		t.Fatalf("background = %+v", background)
	}
	if sess.Status != surface.StatusIdle {
		t.Fatalf("background work is its own fact; status = %s", sess.Status)
	}
	claudehosttest.WaitFor(t, "the tasks to end", func() bool {
		return listedSession(t, c, id).BackgroundTasks == nil
	})
}

func backgroundTranscript(t *testing.T, lines ...any) string {
	t.Helper()
	var data []byte
	for _, line := range lines {
		encoded, err := json.Marshal(line)
		if err != nil {
			t.Fatal(err)
		}
		data = append(append(data, encoded...), '\n')
	}
	return writeTranscript(t, string(data))
}

func backgroundCall(at time.Time, id, name string, input map[string]any) map[string]any {
	return map[string]any{"type": "assistant", "timestamp": at.Format(time.RFC3339Nano), "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}}}}
}

func backgroundResult(at time.Time, id string, result map[string]any) map[string]any {
	return map[string]any{"type": "user", "timestamp": at.Format(time.RFC3339Nano), "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": "started"}}}, "toolUseResult": result}
}

func backgroundNotification(at time.Time, task string) map[string]any {
	return map[string]any{"type": "queue-operation", "operation": "enqueue", "timestamp": at.Format(time.RFC3339Nano), "content": fmt.Sprintf("<task-notification>\n<task-id>%s</task-id>\n<status>completed</status>\n</task-notification>", task)}
}

func terminalBackground(t *testing.T, status string, lines ...any) (*Claude, *surface.Session, string) {
	t.Helper()
	fixture := claudetest.New(t)
	fixture.RecordStatus("conversation-test", fixture.Home, "", status, "", time.Now().Add(-time.Second))
	path := backgroundTranscript(t, lines...)
	return NewClaude(nil, fixture.Home), &surface.Session{ID: "conversation-test", Surface: surface.KindClaude, PID: fixture.PID, Transcript: path}, path
}

func TestATerminalSessionsBackgroundTasksComeFromItsTranscript(t *testing.T) {
	now := time.Now().UTC().Add(time.Second)
	c, sess, path := terminalBackground(t, "shell",
		backgroundCall(now, "call-shell", "Bash", map[string]any{"command": "go test ./...", "description": "Run the tests", "run_in_background": true}),
		backgroundResult(now, "call-shell", map[string]any{"backgroundTaskId": "b-shell"}),
		backgroundCall(now, "call-fg", "Bash", map[string]any{"command": "ls"}),
		backgroundResult(now, "call-fg", map[string]any{"stdout": "x"}),
		backgroundCall(now.Add(time.Second), "call-agent", "Agent", map[string]any{"description": "Review the diff", "run_in_background": true}),
		backgroundResult(now.Add(time.Second), "call-agent", map[string]any{"isAsync": true, "agentId": "a-review"}),
		backgroundCall(now.Add(2*time.Second), "call-monitor", "Monitor", map[string]any{"description": "Watch CI"}),
		backgroundResult(now.Add(2*time.Second), "call-monitor", map[string]any{"taskId": "m-ci"}),
		backgroundCall(now.Add(3*time.Second), "call-done", "Bash", map[string]any{"command": "sleep 1", "run_in_background": true}),
		backgroundResult(now.Add(3*time.Second), "call-done", map[string]any{"backgroundTaskId": "b-done"}),
		backgroundNotification(now.Add(4*time.Second), "b-done"),
	)
	background := c.claudeBackground(context.Background(), sess)
	if background == nil || !background.Running || background.Source != surface.BackgroundSourceTranscript || !background.Since.Equal(now) || len(background.Tasks) != 3 {
		t.Fatalf("background = %+v", background)
	}
	want := []surface.BackgroundTask{
		{ID: "b-shell", Kind: surface.BackgroundShell, Description: "Run the tests", Since: now},
		{ID: "a-review", Kind: surface.BackgroundAgent, Description: "Review the diff", Since: now.Add(time.Second)},
		{ID: "m-ci", Kind: surface.BackgroundMonitor, Description: "Watch CI", Since: now.Add(2 * time.Second)},
	}
	for i := range want {
		if got := background.Tasks[i]; got.ID != want[i].ID || got.Kind != want[i].Kind || got.Description != want[i].Description || !got.Since.Equal(want[i].Since) {
			t.Fatalf("task %d = %+v, want %+v", i, got, want[i])
		}
	}
	appendTranscript(t, path, mustJSON(t, backgroundCall(now.Add(5*time.Second), "call-stop", "TaskStop", map[string]any{"task_id": "m-ci"}))+"\n"+
		mustJSON(t, backgroundNotification(now.Add(6*time.Second), "a-review"))+"\n"+
		mustJSON(t, map[string]any{"type": "attachment", "attachment": map[string]any{"type": "queued_command", "prompt": "<task-notification>\n<task-id>b-shell</task-id>\n</task-notification>"}})+"\n")
	if background := c.claudeBackground(context.Background(), sess); background != nil && len(background.Tasks) > 0 {
		t.Fatalf("every task ended: %+v", background)
	}
}

func TestATerminalSessionsShellTasksCountOnlyWhileClaudeSaysAShellRuns(t *testing.T) {
	now := time.Now().UTC().Add(time.Second)
	lines := []any{
		backgroundCall(now, "call-shell", "Bash", map[string]any{"command": "sleep 60", "run_in_background": true}),
		backgroundResult(now, "call-shell", map[string]any{"backgroundTaskId": "b-shell"}),
	}
	c, sess, _ := terminalBackground(t, "idle", lines...)
	if background := c.claudeBackground(context.Background(), sess); background != nil {
		t.Fatalf("Claude reports no shell running: %+v", background)
	}
	c, sess, _ = terminalBackground(t, "shell")
	background := c.claudeBackground(context.Background(), sess)
	if background == nil || !background.Running || background.Source != surface.BackgroundSourceStatus || len(background.Tasks) != 0 || background.Since.IsZero() {
		t.Fatalf("a shell Claude reports but the transcript does not name still runs: %+v", background)
	}
}

func TestATerminalSessionIgnoresTasksStartedBeforeItsProcess(t *testing.T) {
	before := time.Now().UTC().Add(-time.Hour)
	c, sess, _ := terminalBackground(t, "shell",
		backgroundCall(before, "call-old", "Agent", map[string]any{"description": "From an earlier process", "run_in_background": true}),
		backgroundResult(before, "call-old", map[string]any{"isAsync": true, "agentId": "a-old"}),
	)
	background := c.claudeBackground(context.Background(), sess)
	if background == nil || background.Source != surface.BackgroundSourceStatus || len(background.Tasks) != 0 {
		t.Fatalf("background = %+v", background)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
