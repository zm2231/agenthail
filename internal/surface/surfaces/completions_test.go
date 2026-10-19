package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/zm2231/agenthail/internal/signin"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func appendCompletionTranscript(t *testing.T, path, id, text, reason string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, record := range []any{
		map[string]any{"type": "user", "uuid": "input-" + id, "message": map[string]any{"content": "request " + id}},
		map[string]any{"type": "assistant", "uuid": "record-" + id, "message": map[string]any{"id": id, "stop_reason": reason, "content": []any{map[string]any{"type": "text", "text": text}}}},
		map[string]any{"type": "system", "subtype": "stop_hook_summary"},
	} {
		if err := json.NewEncoder(f).Encode(record); err != nil {
			t.Fatal(err)
		}
	}
}

func TestClaudeCompletionReplayPreservesBurstAndPartialRecordAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	appendCompletionTranscript(t, path, "old", "old response", "end_turn")
	session := &surface.Session{ID: "source", Transcript: path}
	reader := NewClaude(nil, t.TempDir())
	baseline, err := reader.ReadCompletions(context.Background(), session, "")
	if err != nil || len(baseline.Events) != 0 {
		t.Fatalf("baseline=%+v err=%v", baseline, err)
	}
	for i := 1; i <= 7; i++ {
		appendCompletionTranscript(t, path, fmt.Sprint(i), fmt.Sprintf("reply %d", i), "end_turn")
	}
	appendCompletionTranscript(t, path, "failed", "partial", "stop_sequence")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"type":"system","subtype":"turn_duration"}`)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	batch, err := NewClaude(nil, t.TempDir()).ReadCompletions(context.Background(), session, baseline.Cursor)
	if err != nil || len(batch.Events) != 7 {
		t.Fatalf("events=%+v err=%v", batch.Events, err)
	}
	for i, event := range batch.Events {
		if event.ID != fmt.Sprint(i+1) || event.Text != fmt.Sprintf("reply %d", i+1) {
			t.Fatalf("event=%+v", event)
		}
	}
	replay, err := reader.ReadCompletions(context.Background(), session, baseline.Cursor)
	if err != nil || len(replay.Events) != 7 || replay.Cursor != batch.Cursor {
		t.Fatalf("retry did not replay identical batch: %+v %v", replay, err)
	}
	f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	next, err := reader.ReadCompletions(context.Background(), session, batch.Cursor)
	if err != nil || len(next.Events) != 0 {
		t.Fatalf("interrupted turn relayed by duration marker: %+v %v", next, err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadCompletions(context.Background(), session, next.Cursor); err == nil {
		t.Fatal("truncation silently reset the cursor")
	}
}

type completionPages struct {
	calls []string
	fail  bool
}

func (c *completionPages) Close() error { return nil }
func (c *completionPages) Request(_ context.Context, method string, params map[string]any, _ time.Duration) (map[string]any, error) {
	if _, ok := params["page"]; ok {
		return nil, fmt.Errorf("protocol uses top-level pagination fields")
	}
	if params["limit"] != 100 {
		return nil, fmt.Errorf("protocol requires explicit page limit")
	}
	if method == "thread/turns/list" && (params["itemsView"] != "notLoaded" || params["sortDirection"] != "desc") {
		return nil, fmt.Errorf("turn request must not accept summary items")
	}
	if method == "thread/items/list" && params["sortDirection"] != "asc" {
		return nil, fmt.Errorf("item order must be ascending")
	}
	cursor, _ := params["cursor"].(string)
	c.calls = append(c.calls, method+":"+cursor)
	if c.fail && cursor != "" {
		return nil, fmt.Errorf("provider disconnected")
	}
	var data []any
	next := ""
	if method == "thread/turns/list" {
		if cursor == "" {
			data = []any{map[string]any{"id": "active", "status": "inProgress"}}
			for i := 7; i >= 4; i-- {
				data = append(data, map[string]any{"id": fmt.Sprint(i), "status": "completed", "completedAt": int64(1792000000)})
			}
			next = "older"
		} else {
			for i := 3; i >= 0; i-- {
				data = append(data, map[string]any{"id": fmt.Sprint(i), "status": "completed", "completedAt": int64(1792000000)})
			}
		}
	} else if method == "thread/items/list" {
		if cursor == "" {
			data = []any{map[string]any{"item": map[string]any{"id": "commentary", "type": "agentMessage", "phase": "commentary", "text": "working"}}}
			next = "final"
		} else {
			data = []any{map[string]any{"item": map[string]any{"id": "answer", "type": "agentMessage", "phase": "final_answer", "text": "answer " + params["turnId"].(string)}}}
		}
	} else {
		return nil, fmt.Errorf("unexpected request %s", method)
	}
	return map[string]any{"result": map[string]any{"data": data, "nextCursor": next}}, nil
}

func TestCodexCompletionReplayPagesTurnsAndItemsWithoutCollapsing(t *testing.T) {
	client := &completionPages{}
	batch, err := NewCodex("").readCompletions(context.Background(), client, "thread", `"0"`)
	if err != nil || len(batch.Events) != 7 || batch.Cursor != `"7"` {
		t.Fatalf("batch=%+v err=%v", batch, err)
	}
	for i, event := range batch.Events {
		if event.ID != fmt.Sprint(i+1) || event.Text != fmt.Sprintf("answer %d", i+1) || event.CompletedAt.Unix() != 1792000000 {
			t.Fatalf("event=%+v", event)
		}
	}
	for _, test := range []struct {
		name, cursor string
		fail         bool
	}{{"missing boundary", `"missing"`, false}, {"partial read", `"0"`, true}} {
		t.Run(test.name, func(t *testing.T) {
			batch, err := NewCodex("").readCompletions(context.Background(), &completionPages{fail: test.fail}, "thread", test.cursor)
			if err == nil || batch.Cursor != "" || len(batch.Events) != 0 {
				t.Fatalf("partial capture accepted: %+v %v", batch, err)
			}
		})
	}
	baseline, err := NewCodex("").readCompletions(context.Background(), &completionPages{}, "thread", "")
	if err != nil || len(baseline.Events) != 0 || !strings.Contains(baseline.Cursor, "7") {
		t.Fatalf("baseline=%+v err=%v", baseline, err)
	}
}

func TestNotionCompletionReplayPreservesBurstAndInputProvenance(t *testing.T) {
	original := notionRecordRequest
	t.Cleanup(func() { notionRecordRequest = original })
	ids := []string{"old"}
	missing := ""
	toolOnly := ""
	notionRecordRequest = func(_ context.Context, _, _ string, _ map[string]string, body string, _ CookieSource, _ time.Duration) (int, string, error) {
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
