package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func overlappingCallsRollout() []string {
	return []string{
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","name":"exec","call_id":"call-a","input":"text((await tools.exec_command({cmd:\"make a\"})).output)"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","name":"exec","call_id":"call-b","input":"text(await tools.mcp__demo__lookup({query:\"b\"}))"}}`,
		`{"type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-1","item":{"type":"McpToolCall","id":"exec-b","server":"demo","tool":"lookup","arguments":{"query":"b"},"status":"completed","result":{"content":[{"type":"text","text":"found b"}]}}}}`,
		`{"type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-1","item":{"type":"CommandExecution","id":"exec-a","command":["/bin/zsh","-lc","make a"],"aggregated_output":"built a\n","exit_code":0,"status":"completed"}}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call-b","output":[{"type":"input_text","text":"Script completed\nWall time 0.1 seconds\nOutput:\nfound b"}]}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call-a","output":[{"type":"input_text","text":"Script completed\nWall time 0.2 seconds\nOutput:\nbuilt a\n"}]}}`,
	}
}

func TestOverlappingCodeModeCallsKeepTheirOwnActions(t *testing.T) {
	path := timelineFixture(t, strings.Join(overlappingCallsRollout(), "\n")+"\n")
	page, err := readTimeline(context.Background(), path, "codex", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	paged := codexServedShape(page.Items)
	streamed := codexServedShape(codexStreamedItems(t, path))
	pagedJSON, _ := json.Marshal(paged)
	if streamedJSON, _ := json.Marshal(streamed); string(pagedJSON) != string(streamedJSON) {
		t.Fatalf("paged=%s\nstreamed=%s", pagedJSON, streamedJSON)
	}
	want := []surface.TimelineItem{
		{Kind: "toolCall", Title: "exec_command", Text: `{"cmd":"make a"}`, CallID: "call-a"},
		{Kind: "toolCall", Title: "mcp__demo__lookup", Text: `{"query":"b"}`, CallID: "call-b"},
		{Kind: "toolResult", Title: "Tool result", Text: "found b", CallID: "call-b"},
		{Kind: "toolResult", Title: "Tool result", Text: "built a\n", CallID: "call-a"},
	}
	if len(paged) != len(want) {
		t.Fatalf("paged=%s", pagedJSON)
	}
	for index := range want {
		got := paged[index]
		got.ID, got.Timestamp = "", ""
		if !reflect.DeepEqual(got, want[index]) {
			t.Fatalf("item %d=%+v want %+v", index, got, want[index])
		}
	}
}

func TestAStreamResumingInsideACallServesThePagesRows(t *testing.T) {
	lines := overlappingCallsRollout()
	path := timelineFixture(t, strings.Join(lines[:3], "\n")+"\n")
	head, err := readTimeline(context.Background(), path, "codex", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(head.CodexToolWindow.Open) != 2 || !head.TranscriptOffsetSet {
		t.Fatalf("window=%+v offset=%v", head.CodexToolWindow, head.TranscriptOffsetSet)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(strings.Join(lines[3:], "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()
	session := &surface.Session{ID: "resume", Transcript: path, TranscriptOffset: head.TranscriptOffset, TranscriptOffsetSet: true, TranscriptIdentity: head.TranscriptIdentity, CodexCurrentTurnID: head.CodexCurrentTurnID, CodexToolWindow: head.CodexToolWindow}
	items := append([]surface.TimelineItem(nil), head.Items...)
	index := map[string]int{}
	for at, item := range items {
		index[item.ID] = at
	}
	err = NewCodex("").Stream(context.Background(), session, "", func(event surface.StreamEvent) {
		if event.Kind == "commands" {
			return
		}
		item := surface.TimelineItem{ID: event.ID, Kind: event.Kind, Title: event.Title, Text: event.Text, Status: event.Status, CallID: event.CallID, Origin: event.Origin, Timestamp: event.Timestamp.Format(time.RFC3339Nano)}
		if at, ok := index[item.ID]; ok {
			items[at] = item
			return
		}
		index[item.ID] = len(items)
		items = append(items, item)
	}, 20*time.Millisecond)
	if !errors.Is(err, surface.ErrStreamWindow) {
		t.Fatalf("stream error=%v", err)
	}
	full, err := readTimeline(context.Background(), path, "codex", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	resumed, _ := json.Marshal(codexServedShape(items))
	if whole, _ := json.Marshal(codexServedShape(full.Items)); string(resumed) != string(whole) {
		t.Fatalf("resumed=%s\nwhole=%s", resumed, whole)
	}
}
