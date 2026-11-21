package notionsurface

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/browsercookie"
)

func TestNotionCompletionReplayPreservesBurstAndInputProvenance(t *testing.T) {
	original := notionRecordRequest
	t.Cleanup(func() { notionRecordRequest = original })
	ids := []string{"old"}
	missing := ""
	toolOnly := ""
	notionRecordRequest = func(_ context.Context, _, _ string, _ map[string]string, body string, _ browsercookie.Source, _ time.Duration) (int, string, error) {
		if strings.Contains(body, `"table":"thread"`) {
			payload, _ := json.Marshal(map[string]any{"recordMap": map[string]any{"thread": map[string]any{"source": map[string]any{"value": map[string]any{"value": map[string]any{"messages": ids}}}}}})
			return 200, string(payload), nil
		}
		records := map[string]any{}
		for _, id := range ids {
			if id == missing {
				continue
			}
			kind := "agent-inference"
			var value any = []map[string]string{{"type": "text", "content": "answer " + id}, {"type": "text", "content": "details"}}
			if id == toolOnly {
				value = []map[string]string{{"type": "tool-call", "content": "working"}, {"type": "text", "content": "  "}}
			}
			if strings.HasPrefix(id, "user") {
				kind = "user"
				value = [][]string{{"[agenthail relay hops=3 id=7 turn=input source=upstream] request"}}
			}
			records[id] = map[string]any{"value": map[string]any{"value": map[string]any{"step": map[string]any{"type": kind, "value": value}}}}
		}
		payload, _ := json.Marshal(map[string]any{"recordMap": map[string]any{"thread_message": records}})
		return 200, string(payload), nil
	}
	reader := NewNotion(signin.Fixed{Browser: signin.Chrome}, "00000000-0000-0000-0000-000000000000", "user")
	session := &surface.Session{ID: "source", Surface: surface.KindNotion}
	baseline, err := reader.ReadCompletions(context.Background(), session, "")
	if err != nil || len(baseline.Events) != 0 {
		t.Fatalf("baseline=%+v err=%v", baseline, err)
	}
	ids = append(ids, "user-one", "one", "user-two", "two")
	batch, err := reader.ReadCompletions(context.Background(), session, baseline.Cursor)
	if err != nil || len(batch.Events) != 2 || batch.Events[0].ID != "one" || batch.Events[1].ID != "two" || batch.Events[1].Text != "answer two\ndetails" || batch.Events[1].InputID != "user-two" || !strings.Contains(batch.Events[1].User, "hops=3") {
		t.Fatalf("batch=%+v err=%v", batch, err)
	}
	missing = "one"
	if batch, err := reader.ReadCompletions(context.Background(), session, baseline.Cursor); err == nil || batch.Cursor != "" {
		t.Fatalf("missing record advanced capture: %+v %v", batch, err)
	}
	id, reply, err := reader.latestAgentReply(context.Background(), session)
	if err != nil || id != "two" || reply == nil || !reply.Done {
		t.Fatalf("missing historical record broke ordinary reply: %s %+v %v", id, reply, err)
	}
	if next, err := reader.ReadCompletions(context.Background(), session, batch.Cursor); err != nil || len(next.Events) != 0 {
		t.Fatalf("missing record before boundary blocked capture: %+v %v", next, err)
	}
	toolOnly = "working"
	ids = append(ids, "user-working", toolOnly)
	if next, err := reader.ReadCompletions(context.Background(), session, batch.Cursor); err != nil || len(next.Events) != 0 || next.Cursor != batch.Cursor {
		t.Fatalf("tool-only record advanced capture: %+v %v", next, err)
	}
	id, reply, err = reader.latestAgentReply(context.Background(), session)
	if err != nil || id != "two" || reply == nil || !reply.Done {
		t.Fatalf("tool-only record replaced ordinary reply: %s %+v %v", id, reply, err)
	}
	toolOnly = ""
	next, err := reader.ReadCompletions(context.Background(), session, batch.Cursor)
	if err != nil || len(next.Events) != 1 || next.Events[0].ID != "working" || next.Events[0].InputID != "user-working" {
		t.Fatalf("textual completion lost after tool-only record: %+v %v", next, err)
	}
	if replay, err := reader.ReadCompletions(context.Background(), session, next.Cursor); err != nil || len(replay.Events) != 0 {
		t.Fatalf("textual completion replayed: %+v %v", replay, err)
	}
}
