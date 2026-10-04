package surfaces

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestNotionReadSessionUsesBoundedProviderMessageIDs(t *testing.T) {
	original := notionRecordRequest
	t.Cleanup(func() { notionRecordRequest = original })
	var requested [][]string
	notionRecordRequest = func(_ context.Context, _ string, _ string, _ map[string]string, body, _, _ string, _ time.Duration) (int, string, error) {
		var request struct {
			Requests []struct {
				Pointer struct {
					Table string `json:"table"`
					ID    string `json:"id"`
				} `json:"pointer"`
			} `json:"requests"`
		}
		if err := json.Unmarshal([]byte(body), &request); err != nil {
			t.Fatalf("request: %v", err)
		}
		if len(request.Requests) == 1 && request.Requests[0].Pointer.Table == "thread" {
			return 200, `{"recordMap":{"thread":{"thread-1":{"value":{"value":{"messages":["m1","m2","m3","m4","m5","m6"]}}}}}}`, nil
		}
		ids := make([]string, 0, len(request.Requests))
		for _, item := range request.Requests {
			ids = append(ids, item.Pointer.ID)
		}
		requested = append(requested, ids)
		messages := map[string]any{}
		for _, id := range ids {
			index := strings.TrimPrefix(id, "m")
			role := "user"
			value := any([][]string{{"question " + index}})
			if id == "m2" || id == "m4" || id == "m6" {
				role = "agent-inference"
				value = []map[string]string{{"type": "text", "content": "answer " + index}}
			}
			messages[id] = map[string]any{"value": map[string]any{"value": map[string]any{"step": map[string]any{"type": role, "createdAt": "2026-10-04T12:00:00Z", "value": value}}}}
		}
		response, err := json.Marshal(map[string]any{"recordMap": map[string]any{"thread_message": messages}})
		if err != nil {
			t.Fatal(err)
		}
		return 200, string(response), nil
	}

	notion := NewNotion("00000000-0000-0000-0000-000000000000", "user")
	read, err := surface.ReadSession(context.Background(), notion, &surface.Session{ID: "thread-1", Surface: surface.KindNotion}, surface.SessionReadRequest{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if read.Source != "notion-remote" || read.NextBefore != 2 || len(read.Items) != 4 || len(read.Exchanges) != 2 {
		t.Fatalf("read=%+v", read)
	}
	if read.Items[0].ID != "m3" || read.Items[3].ID != "m6" || read.Exchanges[0].User != "question 3" || read.Exchanges[1].Assistant != "answer 6" {
		t.Fatalf("items=%+v exchanges=%+v", read.Items, read.Exchanges)
	}
	if len(requested) != 1 || len(requested[0]) != 4 || requested[0][0] != "m3" || requested[0][3] != "m6" {
		t.Fatalf("provider request=%v", requested)
	}

	older, err := notion.ReadSession(context.Background(), &surface.Session{ID: "thread-1", Surface: surface.KindNotion}, surface.SessionReadRequest{Limit: 2, Before: read.NextBefore})
	if err != nil {
		t.Fatal(err)
	}
	if len(older.Items) != 2 || older.Items[0].ID != "m1" || older.Items[1].ID != "m2" || older.NextBefore != 0 {
		t.Fatalf("older=%+v", older)
	}
}

func TestNotionTailDelegatesReadSession(t *testing.T) {
	original := notionRecordRequest
	t.Cleanup(func() { notionRecordRequest = original })
	calls := 0
	notionRecordRequest = func(_ context.Context, _ string, _ string, _ map[string]string, body, _, _ string, _ time.Duration) (int, string, error) {
		calls++
		if calls == 1 {
			return 200, `{"recordMap":{"thread":{"thread-1":{"value":{"value":{"messages":["m1","m2"]}}}}}}`, nil
		}
		return 200, `{"recordMap":{"thread_message":{"m1":{"value":{"value":{"step":{"type":"user","value":[["question"]]}}}},"m2":{"value":{"value":{"step":{"type":"agent-inference","value":[{"type":"text","content":"answer"}]}}}}}}}`, nil
	}
	read, err := (&Notion{spaceID: "00000000-0000-0000-0000-000000000000", userID: "user"}).Tail(context.Background(), &surface.Session{ID: "thread-1"}, 1)
	if err != nil || len(read) != 1 || read[0].Assistant != "answer" {
		t.Fatalf("tail=%+v err=%v", read, err)
	}
}
