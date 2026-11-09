package surfaces

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

type hangingModel struct{ done chan struct{} }

func (m hangingModel) ServeHTTP(http.ResponseWriter, *http.Request) { <-m.done }

func realThread(t *testing.T, client codexClient) string {
	t.Helper()
	started, err := client.Request(context.Background(), "thread/start", map[string]any{"cwd": t.TempDir()}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return gjson.Get(codexJSON(started), "result.thread.id").String()
}

func TestACodexCompactionHoldsReloadsOnlyUntilItsTurnEnds(t *testing.T) {
	realManagedCodex(t)
	ctx := context.Background()
	client, err := dialManagedCodex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	threadID := realThread(t, client)
	realTurn(t, client, threadID, "first")
	codex := &Codex{}
	if _, err := codex.startTurn(ctx, client, threadID, "thread/compact/start", map[string]any{"threadId": threadID}, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		busy, err := codex.turnInFlight(ctx, client, threadID)
		if err != nil {
			t.Fatal(err)
		}
		if !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the thread still reads as working long after its compaction ended")
		}
	}
	if compactions := rolloutCompactions(t, threadID); compactions != 1 {
		t.Fatalf("compactions=%d", compactions)
	}
}

func TestATurnStartedBeforeTheCodexAppServerRestartedIsNotRunning(t *testing.T) {
	model := hangingModel{done: make(chan struct{})}
	restart := realManagedCodexWith(t, model)
	t.Cleanup(func() { close(model.done) })
	wait := codexUnsettledTurnWait
	codexUnsettledTurnWait = 0
	t.Cleanup(func() { codexUnsettledTurnWait = wait })
	ctx := context.Background()
	client, err := dialManagedCodex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	threadID := realThread(t, client)
	codex := &Codex{}
	input := map[string]any{"threadId": threadID, "input": []any{map[string]any{"type": "text", "text": "hang"}}}
	if _, err := codex.startTurn(ctx, client, threadID, "turn/start", input, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	client.Close()
	restart()
	client, err = dialManagedCodex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	if _, err := client.Request(ctx, "thread/resume", map[string]any{"threadId": threadID}, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if id, err := codex.activeTurnID(ctx, client, threadID); err != nil || id != "" {
		t.Fatalf("a turn the restarted app-server lost reads as running: %q %v", id, err)
	}
}
