package surfaces

import (
	"context"
	"testing"
)

const syntheticNotification = `<task-notification>\n<task-id>task-7</task-id>\n<tool-use-id>toolu_synthetic</tool-use-id>\n<output-file>/tmp/synthetic.output</output-file>\n<status>completed</status>\n<summary>Background build finished</summary>\n</task-notification>`

func TestClaudeTimelineClassifiesInjectedPrompts(t *testing.T) {
	path := timelineFixture(t, `{"type":"user","uuid":"u1","origin":{"kind":"human"},"message":{"content":"Run the build"}}
{"type":"attachment","uuid":"q1","attachment":{"type":"queued_command","prompt":"`+syntheticNotification+`"}}
{"type":"attachment","uuid":"q2","attachment":{"type":"queued_command","prompt":"also check lint","origin":{"kind":"human"}}}
{"type":"queue-operation","operation":"enqueue","content":"`+syntheticNotification+`"}
{"type":"user","uuid":"u2","origin":{"kind":"task-notification","producer":"session-task"},"message":{"content":"`+syntheticNotification+`"}}
{"type":"user","uuid":"u3","origin":{"kind":"peer","name":"reviewer","msg_id":"msg-1","body":"Please rebase"},"message":{"content":"Another Claude session sent a message: <cross-session-message from=\"uds:/tmp/synthetic.sock\" from-name=\"reviewer\">Please rebase</cross-session-message>"}}
{"type":"attachment","uuid":"q3","attachment":{"type":"queued_command","prompt":"<cross-session-message from=\"uds:/tmp/synthetic.sock\" from-name=\"reviewer\">Please rebase</cross-session-message>","origin":{"kind":"peer","name":"reviewer","msg_id":"msg-1","body":"Please rebase"}}}
{"type":"user","uuid":"u4","origin":{"kind":"auto-continuation"},"message":{"content":"Continue the task you were working on."}}
{"type":"user","uuid":"u5","origin":{"kind":"human"},"message":{"content":"I pasted this: `+syntheticNotification+`"}}
{"type":"user","uuid":"u6","message":{"content":"legacy prompt"}}
{"type":"user","uuid":"u7","message":{"content":[{"type":"tool_result","tool_use_id":"call-1","content":"ok"}]}}
`)
	page, err := readTimeline(context.Background(), path, "claude", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	type want struct {
		kind, role, origin, sender, text, status, callID string
	}
	wants := []want{
		{"message", "user", "", "", "Run the build", "", ""},
		{"event", "system", "task-notification", "", "Background build finished", "completed", "toolu_synthetic"},
		{"message", "peer", "peer", "reviewer", "Please rebase", "", ""},
		{"event", "system", "auto-continuation", "", "Continue the task you were working on.", "", ""},
		{"message", "user", "", "", "I pasted this: " + "<task-notification>\n<task-id>task-7</task-id>\n<tool-use-id>toolu_synthetic</tool-use-id>\n<output-file>/tmp/synthetic.output</output-file>\n<status>completed</status>\n<summary>Background build finished</summary>\n</task-notification>", "", ""},
		{"message", "user", "", "", "legacy prompt", "", ""},
		{"toolResult", "", "", "", "ok", "", "call-1"},
	}
	if len(page.Items) != len(wants) {
		t.Fatalf("items=%d %+v", len(page.Items), page.Items)
	}
	for index, expected := range wants {
		item := page.Items[index]
		got := want{item.Kind, item.Role, item.Origin, item.Sender, item.Text, item.Status, item.CallID}
		if expected.kind == "toolResult" {
			got.role = ""
		}
		if got != expected {
			t.Fatalf("item %d = %+v want %+v", index, got, expected)
		}
	}
	if page.Items[1].ID != "task-notification:task-7:completed" || page.Items[2].ID != "peer:msg-1" {
		t.Fatalf("injection identities %q %q", page.Items[1].ID, page.Items[2].ID)
	}
	for _, exchange := range page.Exchanges {
		if exchange.User == "Please rebase" {
			t.Fatal("peer body leaked into exchanges as user text")
		}
	}
}

func TestClaudeQueuedHumanPromptKeepsExistingPresentation(t *testing.T) {
	items := claudeTimelineItems(map[string]any{"type": "attachment", "attachment": map[string]any{"type": "queued_command", "prompt": "follow up", "origin": map[string]any{"kind": "human"}}})
	if len(items) != 0 {
		t.Fatalf("items=%+v", items)
	}
	unknown := claudeTimelineItems(map[string]any{"type": "user", "origin": map[string]any{"kind": "scheduled"}, "message": map[string]any{"content": "nightly check"}})
	if len(unknown) != 1 || unknown[0].Kind != "event" || unknown[0].Origin != "scheduled" || unknown[0].Role != "system" {
		t.Fatalf("unknown=%+v", unknown)
	}
}
