package codexsurface

import (
	"context"
	"fmt"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
	"strings"
	"testing"
	"time"
)

func TestCodexCompletionReplayPagesTurnsAndItemsWithoutCollapsing(t *testing.T) {
	client := &completionPages{}
	batch, err := surfacetest.InDaemon(NewCodex("")).readCompletions(context.Background(), client, "thread", `"0"`)
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
			batch, err := surfacetest.InDaemon(NewCodex("")).readCompletions(context.Background(), &completionPages{fail: test.fail}, "thread", test.cursor)
			if err == nil || batch.Cursor != "" || len(batch.Events) != 0 {
				t.Fatalf("partial capture accepted: %+v %v", batch, err)
			}
		})
	}
	baseline, err := surfacetest.InDaemon(NewCodex("")).readCompletions(context.Background(), &completionPages{}, "thread", "")
	if err != nil || len(baseline.Events) != 0 || !strings.Contains(baseline.Cursor, "7") {
		t.Fatalf("baseline=%+v err=%v", baseline, err)
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
