package surfaces

import (
	"context"
	"testing"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
)

func unconfirmedTurnFixture(t *testing.T, status string) (*Codex, *surface.Session, *codextest.Daemon) {
	t.Helper()
	daemon := codextest.Start(t, func(method string, _ map[string]any) map[string]any {
		if method != "thread/items/list" {
			return nil
		}
		return map[string]any{"data": []any{map[string]any{"type": "userMessage", "text": "two"}, map[string]any{"type": "agentMessage", "text": "the real ending"}}}
	})
	daemon.AddThread(codextest.Thread{ID: "thread", Turns: []codextest.Turn{
		{ID: "t1", Status: "completed", User: "one", Assistant: "first reply"},
		{ID: "t2", Status: status, User: "two"},
	}})
	return isolatedManagedRuntime(t), &surface.Session{ID: "thread", Surface: surface.KindCodex}, daemon
}

func TestARunningTurnReadByANonOwnerIsNotReportedFailed(t *testing.T) {
	codex, session, daemon := unconfirmedTurnFixture(t, "interrupted")
	daemon.Unload("thread")
	observation, err := codex.Observe(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if !observation.TurnUnconfirmed || observation.CompletedTurnID != "t1" || observation.TerminalTurnID != "t1" || observation.Reply == nil || observation.Reply.Error != "" {
		t.Fatalf("observation=%+v reply=%+v", observation, observation.Reply)
	}
}

func TestAnInterruptionTheOwnerReportsIsReal(t *testing.T) {
	codex, session, daemon := unconfirmedTurnFixture(t, "interrupted")
	daemon.Load("thread")
	observation, err := codex.Observe(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if observation.TurnUnconfirmed || observation.CompletedTurnID != "t2" || observation.Reply == nil || observation.Reply.Error != codexTurnInterrupted {
		t.Fatalf("observation=%+v reply=%+v", observation, observation.Reply)
	}
}

func TestACompletedTurnReadByANonOwnerIsConfirmed(t *testing.T) {
	codex, session, daemon := unconfirmedTurnFixture(t, "completed")
	daemon.Unload("thread")
	observation, err := codex.Observe(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if observation.TurnUnconfirmed || observation.TerminalTurnID != "t2" {
		t.Fatalf("observation=%+v", observation)
	}
}

// A relay's cursor that moved past the falsely interrupted turn would lose its completion.
func TestARelayStillGetsATurnANonOwnerFirstReadAsInterrupted(t *testing.T) {
	codex, session, daemon := unconfirmedTurnFixture(t, "interrupted")
	daemon.Unload("thread")
	baseline, err := codex.ReadCompletions(context.Background(), session, "")
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Cursor != `"t1"` {
		t.Fatalf("cursor=%s", baseline.Cursor)
	}
	during, err := codex.ReadCompletions(context.Background(), session, baseline.Cursor)
	if err != nil || len(during.Events) != 0 || during.Cursor != baseline.Cursor {
		t.Fatalf("during=%+v err=%v", during, err)
	}
	daemon.AddThread(codextest.Thread{ID: "thread", Turns: []codextest.Turn{
		{ID: "t1", Status: "completed", User: "one", Assistant: "first reply"},
		{ID: "t2", Status: "completed", User: "two", Assistant: "the real ending"},
	}})
	daemon.Unload("thread")
	after, err := codex.ReadCompletions(context.Background(), session, during.Cursor)
	if err != nil || len(after.Events) != 1 || after.Events[0].ID != "t2" || after.Events[0].Text != "the real ending" {
		t.Fatalf("after=%+v err=%v", after, err)
	}
}
