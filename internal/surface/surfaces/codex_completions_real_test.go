package surfaces

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

type slowModel struct{ mockResponses }

func (m *slowModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	time.Sleep(2 * time.Second)
	m.mockResponses.ServeHTTP(w, r)
}

func TestACodexTurnReadWhileItStartsIsStillReportedOnceItCompletes(t *testing.T) {
	realManagedCodexWith(t, &slowModel{})
	ctx := context.Background()
	client, err := dialManagedCodex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	started, err := client.Request(ctx, "thread/start", map[string]any{"cwd": t.TempDir()}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	threadID := gjson.Get(codexJSON(started), "result.thread.id").String()
	realTurn(t, client, threadID, "first")
	reader, err := dialManagedCodex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close() })
	codex := &Codex{}
	batch, err := codex.readCompletions(ctx, reader, threadID, "")
	if err != nil {
		t.Fatal(err)
	}
	cursor := batch.Cursor
	turn, err := client.Request(ctx, "turn/start", map[string]any{"threadId": threadID, "input": []any{map[string]any{"type": "text", "text": "second"}}}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	turnID := gjson.Get(codexJSON(turn), "result.turn.id").String()
	reported := false
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline) && !reported; {
		batch, err := codex.readCompletions(ctx, reader, threadID, cursor)
		if err != nil {
			t.Fatal(err)
		}
		cursor = batch.Cursor
		for _, event := range batch.Events {
			reported = reported || event.ID == turnID
		}
	}
	if !reported {
		t.Fatalf("turn %s completed but was never reported; the cursor moved to %s", turnID, cursor)
	}
}

func TestAnInterruptSentWhileACodexTurnStartsStopsThatTurn(t *testing.T) {
	realManagedCodexWith(t, &slowModel{})
	ctx := context.Background()
	client, err := dialManagedCodex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	started, err := client.Request(ctx, "thread/start", map[string]any{"cwd": t.TempDir()}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	threadID := gjson.Get(codexJSON(started), "result.thread.id").String()
	realTurn(t, client, threadID, "first")
	controller, err := dialManagedCodex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { controller.Close() })
	turn, err := client.Request(ctx, "turn/start", map[string]any{"threadId": threadID, "input": []any{map[string]any{"type": "text", "text": "second"}}}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	turnID := gjson.Get(codexJSON(turn), "result.turn.id").String()
	if err := (&Codex{}).interruptActiveTurn(ctx, controller, threadID, turnID); err != nil {
		t.Fatalf("an interrupt sent while the turn started was refused: %v", err)
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		newest, err := codexNewestTurn(ctx, controller, threadID)
		if err != nil {
			t.Fatal(err)
		}
		if str(newest, "id") == turnID && newest["completedAt"] != nil {
			if status := str(newest, "status"); status != "interrupted" {
				t.Fatalf("the interrupted turn ended %s", status)
			}
			return
		}
	}
	t.Fatal("the turn never ended")
}
