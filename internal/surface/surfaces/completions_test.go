package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
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
	batch, err := inDaemon(NewCodex("")).readCompletions(context.Background(), client, "thread", `"0"`)
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
			batch, err := inDaemon(NewCodex("")).readCompletions(context.Background(), &completionPages{fail: test.fail}, "thread", test.cursor)
			if err == nil || batch.Cursor != "" || len(batch.Events) != 0 {
				t.Fatalf("partial capture accepted: %+v %v", batch, err)
			}
		})
	}
	baseline, err := inDaemon(NewCodex("")).readCompletions(context.Background(), &completionPages{}, "thread", "")
	if err != nil || len(baseline.Events) != 0 || !strings.Contains(baseline.Cursor, "7") {
		t.Fatalf("baseline=%+v err=%v", baseline, err)
	}
}

func TestARelayScanOfManyClaudeSourcesListsTheProjectsOnce(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	fixture := claudetest.New(t)
	c := NewClaude(nil, fixture.Home)
	projects := filepath.Join(fixture.Root, "projects")
	var listings int
	c.transcripts.readDir = func(dir string) ([]os.DirEntry, error) {
		if dir == projects {
			listings++
		}
		return os.ReadDir(dir)
	}
	paths := map[string]string{}
	for i := range 5 {
		id := fmt.Sprintf("0000000%d-0caa-4912-823f-927677775b2b", i)
		path := filepath.Join(projects, fmt.Sprintf("project-%d", i), id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		appendCompletionTranscript(t, path, "old-"+id, "old", "end_turn")
		paths[id] = path
	}
	cursors := map[string]string{}
	scan := c.CompletionScan()
	for id := range paths {
		batch, err := scan.ReadCompletions(context.Background(), &surface.Session{ID: id, Surface: surface.KindClaude}, "")
		if err != nil || batch.Cursor == "" {
			t.Fatalf("%s: baseline=%+v err=%v", id, batch, err)
		}
		cursors[id] = batch.Cursor
	}
	if listings != 1 {
		t.Fatalf("listed the projects %d times for %d sources in one scan", listings, len(paths))
	}
	for id, path := range paths {
		appendCompletionTranscript(t, path, "new-"+id, "reply to "+id, "end_turn")
	}
	scan = c.CompletionScan()
	for id := range paths {
		batch, err := scan.ReadCompletions(context.Background(), &surface.Session{ID: id, Surface: surface.KindClaude}, cursors[id])
		if err != nil || len(batch.Events) != 1 || batch.Events[0].Text != "reply to "+id {
			t.Fatalf("%s: events=%+v err=%v", id, batch.Events, err)
		}
	}
	if listings != 2 {
		t.Fatalf("listed the projects %d times over two scans", listings)
	}
}
