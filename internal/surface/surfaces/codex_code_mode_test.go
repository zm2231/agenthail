package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func codexCodeModeFixture(t *testing.T) string {
	t.Helper()
	record := func(at, typ string, payload map[string]any) string {
		encoded, err := json.Marshal(map[string]any{"timestamp": "2026-10-26T10:00:" + at + "Z", "type": typ, "payload": payload})
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	call := func(at, id, script string) string {
		return record(at, "response_item", map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": id, "status": "completed", "input": script})
	}
	output := func(at, id, header, text string) string {
		return record(at, "response_item", map[string]any{"type": "custom_tool_call_output", "call_id": id, "output": []any{map[string]any{"type": "input_text", "text": header + "\nWall time 1.0 seconds\nOutput:\n"}, map[string]any{"type": "input_text", "text": text}}})
	}
	completed := func(at string, item map[string]any) string {
		return record(at, "event_msg", map[string]any{"type": "item_completed", "turn_id": "turn-1", "item": item})
	}
	command := func(id, cmd, out string, exit int, seconds int) map[string]any {
		return map[string]any{"type": "CommandExecution", "id": id, "command": []any{"/bin/zsh", "-lc", cmd}, "cwd": "file:///work/demo", "aggregated_output": out, "exit_code": exit, "status": "completed", "duration": map[string]any{"secs": seconds, "nanos": 0}}
	}
	lines := []string{
		record("00", "event_msg", map[string]any{"type": "task_started", "turn_id": "turn-1"}),
		record("00", "response_item", map[string]any{"type": "additional_tools", "role": "developer", "tools": []any{}}),
		record("00", "event_msg", map[string]any{"type": "thread_settings_applied", "thread_settings": map[string]any{"model": "demo"}}),
		call("01", "call-ls", `const r = await tools.exec_command({"cmd":"ls -la","workdir":"/work/demo","yield_time_ms":10000});
text(r.output);`),
		completed("01", command("exec-ls", "ls -la", "README.md\n", 0, 0)),
		output("01", "call-ls", "Script completed", "README.md\n"),
		call("02", "call-build", "const r = await tools.exec_command({cmd:\"make build\", yield_time_ms: 30000}); text(r.output);"),
		output("32", "call-build", "Script completed", "compiling\n"),
		call("33", "call-agent", `text(await tools.multi_agent_v1__spawn_agent({message:"Review the parser"}))`),
		completed("33", map[string]any{"type": "CollabAgentToolCall", "id": "exec-agent", "tool": "spawn_agent", "status": "completed", "prompt": "Review the parser", "receiver_agents": []any{map[string]any{"thread_id": "t-2", "agent_nickname": "Ada"}}, "agents_states": map[string]any{"t-2": "pending_init"}}),
		output("33", "call-agent", "Script completed", `{"agent_id":"t-2","nickname":"Ada"}`),
		record("34", "event_msg", map[string]any{"type": "future_event", "detail": map[string]any{"secret": strings.Repeat("x", 64)}}),
		record("35", "event_msg", map[string]any{"type": "task_complete", "turn_id": "turn-1"}),
		completed("50", command("exec-build", "make build", "compiling\nok\n", 0, 48)),
	}
	return timelineFixture(t, strings.Join(lines, "\n")+"\n")
}

func codexStreamedItems(t *testing.T, path string) []surface.TimelineItem {
	t.Helper()
	session := &surface.Session{ID: "code-mode", Transcript: path, TranscriptOffsetSet: true}
	var items []surface.TimelineItem
	index := map[string]int{}
	err := NewCodex("").Stream(context.Background(), session, "", func(event surface.StreamEvent) {
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
	return items
}

func codexServedShape(items []surface.TimelineItem) []surface.TimelineItem {
	var shape []surface.TimelineItem
	for _, item := range items {
		if item.Kind == "done" {
			continue
		}
		at, _ := time.Parse(time.RFC3339Nano, item.Timestamp)
		shape = append(shape, surface.TimelineItem{ID: item.ID, Kind: item.Kind, Title: item.Title, Text: item.Text, Status: item.Status, CallID: item.CallID, Origin: item.Origin, Timestamp: at.Format(time.RFC3339Nano)})
	}
	return shape
}

func TestACodeModeThreadReadsAsToolRowsWhetherPagedOrStreamed(t *testing.T) {
	path := codexCodeModeFixture(t)
	page, err := readTimeline(context.Background(), path, "codex", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	paged := codexServedShape(page.Items)
	streamed := codexServedShape(codexStreamedItems(t, path))
	pagedJSON, _ := json.MarshalIndent(paged, "", " ")
	streamedJSON, _ := json.MarshalIndent(streamed, "", " ")
	if string(pagedJSON) != string(streamedJSON) {
		t.Fatalf("paged=%s\nstreamed=%s", pagedJSON, streamedJSON)
	}
	want := []surface.TimelineItem{
		{Kind: "toolCall", Title: "exec_command", Text: `{"cmd":"ls -la","workdir":"/work/demo"}`, CallID: "call-ls"},
		{Kind: "toolResult", Title: "Tool result", Text: "README.md\n", CallID: "call-ls"},
		{Kind: "toolCall", Title: "exec_command", Text: `{"cmd":"make build"}`, CallID: "call-build"},
		{Kind: "toolResult", Title: "Tool result", Text: "Running in the background.\n\ncompiling", Status: codexBackgroundStatus, CallID: "call-build"},
		{Kind: "toolCall", Title: "spawn_agent", Text: `{"prompt":"Review the parser"}`, CallID: "call-agent"},
		{Kind: "toolResult", Title: "Tool result", Text: "Ada: pending init", CallID: "call-agent"},
		{Kind: "event", Title: "Codex · Future event"},
		{Kind: "event", Title: "Background command", Text: "make build · exit 0 · 48s\n\n```\ncompiling\nok\n```", Status: "completed", CallID: "call-build", Origin: surface.OriginTaskNotification},
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
	if paged[0].ID == paged[2].ID || paged[0].ID == "" {
		t.Fatalf("call rows share or lack ids: %s", pagedJSON)
	}
	notice := surface.PresentTimelineItem(page.Items[len(page.Items)-1])
	if notice.Kind == "done" {
		notice = surface.PresentTimelineItem(page.Items[len(page.Items)-2])
	}
	if notice.Agent == nil || notice.Agent.Kind != surface.AgentKindTask || notice.Agent.Summary != "make build · exit 0 · 48s" {
		t.Fatalf("background notice=%+v", notice)
	}
}

func TestAnAgenthailSendInACodeModeScriptReadsAsAnOutgoingSend(t *testing.T) {
	record := map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "c1", "input": `text((await tools.exec_command({cmd:"agenthail send @lead \"tests pass\""})).output)`}}
	items := codexTimelineItems(record)
	if len(items) != 1 {
		t.Fatalf("items=%+v", items)
	}
	if send := surface.PresentTimelineItem(items[0]).Agent; send == nil || send.Kind != surface.AgentKindSend || send.Peer != "@lead" {
		t.Fatalf("send=%+v item=%+v", send, items[0])
	}
}

func TestAFailedCommandMarksItsCodeModeRowFailed(t *testing.T) {
	lines := []string{
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","name":"exec","call_id":"c1","input":"text((await tools.exec_command({cmd:\"false\"})).output)"}}`,
		`{"type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-1","item":{"type":"CommandExecution","id":"e1","command":["/bin/zsh","-lc","false"],"aggregated_output":"","exit_code":1,"status":"failed"}}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c1","output":[{"type":"input_text","text":"Script completed\nWall time 0.1 seconds\nOutput:\n"}]}}`,
	}
	page, err := readTimeline(context.Background(), timelineFixture(t, strings.Join(lines, "\n")+"\n"), "codex", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	items := codexServedShape(page.Items)
	if len(items) != 2 || items[1].Kind != "toolResult" || items[1].Status != "failed" || items[1].CallID != "c1" {
		t.Fatalf("items=%+v", items)
	}
}
