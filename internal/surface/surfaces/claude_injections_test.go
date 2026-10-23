package surfaces

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const syntheticNotification = `<task-notification>\n<task-id>task-7</task-id>\n<tool-use-id>toolu_synthetic</tool-use-id>\n<output-file>/tmp/synthetic.output</output-file>\n<status>completed</status>\n<summary>Background build finished</summary>\n</task-notification>`

func TestClaudeTaskNotificationsOfOneTaskKeepTheirOwnIdentity(t *testing.T) {
	first := `<task-notification>\n<task-id>monitor-1</task-id>\n<summary>Monitor event: first</summary>\n</task-notification>`
	second := `<task-notification>\n<task-id>monitor-1</task-id>\n<summary>Monitor event: second</summary>\n</task-notification>`
	path := timelineFixture(t, `{"type":"user","uuid":"n1","origin":{"kind":"task-notification"},"message":{"content":"`+first+`"}}
{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"one"}]}}
{"type":"attachment","uuid":"q2","attachment":{"type":"queued_command","prompt":"`+second+`"}}
{"type":"user","uuid":"n2","origin":{"kind":"task-notification"},"message":{"content":"`+second+`"}}
{"type":"assistant","uuid":"a2","message":{"id":"m2","stop_reason":"end_turn","content":[{"type":"text","text":"two"}]}}
`)
	page, err := readTimeline(context.Background(), path, "claude", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]int{}
	for _, item := range page.Items {
		if item.Origin == "task-notification" {
			ids[item.ID]++
		}
	}
	if len(ids) != 2 || len(page.Exchanges) != 2 || page.Exchanges[0].User != "Monitor event: first" || page.Exchanges[1].User != "Monitor event: second" {
		t.Fatalf("ids=%v exchanges=%+v", ids, page.Exchanges)
	}
}

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
		{"message", "user", "", "", "also check lint", "", ""},
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
	if !strings.HasPrefix(page.Items[2].ID, "task-notification:task-7:completed:") || page.Items[3].ID != "peer:msg-1" {
		t.Fatalf("injection identities %q %q", page.Items[2].ID, page.Items[3].ID)
	}
	for _, exchange := range page.Exchanges {
		if exchange.User == "Please rebase" && (exchange.Origin != "peer" || exchange.Sender != "reviewer") {
			t.Fatalf("peer message shown as the person's words: %+v", exchange)
		}
		if strings.HasPrefix(exchange.User, "<task-notification>") && exchange.Origin == "" {
			t.Fatalf("task notification shown as the person's words: %+v", exchange)
		}
	}
}

func TestClaudeQueuedHumanPromptIsThePersonsMessage(t *testing.T) {
	items := claudeTimelineItems(map[string]any{"type": "attachment", "attachment": map[string]any{"type": "queued_command", "prompt": "follow up", "commandMode": "prompt", "origin": map[string]any{"kind": "human"}}})
	if len(items) != 1 || items[0].Kind != "message" || items[0].Role != "user" || items[0].Text != "follow up" || items[0].Origin != "" {
		t.Fatalf("items=%+v", items)
	}
	unknown := claudeTimelineItems(map[string]any{"type": "user", "origin": map[string]any{"kind": "scheduled"}, "message": map[string]any{"content": "nightly check"}})
	if len(unknown) != 1 || unknown[0].Kind != "event" || unknown[0].Origin != "scheduled" || unknown[0].Role != "system" {
		t.Fatalf("unknown=%+v", unknown)
	}
}

// Claude Code records a person's message absorbed into a running turn only as
// a queued_command attachment; no user record follows it.
func TestClaudeMidTurnMessageIsInTheTimelineAtItsTime(t *testing.T) {
	path := timelineFixture(t, `{"type":"user","uuid":"u1","timestamp":"2026-10-23T08:29:00Z","origin":{"kind":"human"},"message":{"content":"Run the build"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-10-23T08:29:10Z","message":{"id":"m1","content":[{"type":"tool_use","id":"call-1","name":"Bash","input":{"command":"make"}}]}}
{"type":"queue-operation","operation":"enqueue","timestamp":"2026-10-23T08:29:27Z","content":"store it in the task"}
{"type":"queue-operation","operation":"remove","timestamp":"2026-10-23T08:29:31Z","content":"store it in the task","reason":"absorbed_mid_turn","commandUuid":"cmd-1","deliveryId":"delivery-1"}
{"type":"user","uuid":"r1","timestamp":"2026-10-23T08:29:31Z","message":{"content":[{"type":"tool_result","tool_use_id":"call-1","content":"ok"}]}}
{"type":"attachment","uuid":"q1","timestamp":"2026-10-23T08:29:27Z","attachment":{"type":"queued_command","prompt":"store it in the task","source_uuid":"cmd-1","delivery_id":"delivery-1","commandMode":"prompt","origin":{"kind":"human"},"timestamp":"2026-10-23T08:29:27Z","humanTurn":true}}
{"type":"assistant","uuid":"a2","timestamp":"2026-10-23T08:29:40Z","message":{"id":"m2","stop_reason":"end_turn","content":[{"type":"text","text":"Stored."}]}}
`)
	page, err := readTimeline(context.Background(), path, "claude", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, item := range page.Items {
		if item.Kind == "message" {
			texts = append(texts, item.Role+":"+item.Text)
		}
		if item.Text == "store it in the task" && item.Timestamp != "2026-10-23T08:29:27Z" {
			t.Fatalf("mid-turn message timestamp %q", item.Timestamp)
		}
	}
	if strings.Join(texts, "|") != "user:Run the build|user:store it in the task|assistant:Stored." {
		t.Fatalf("messages=%q", texts)
	}
}

func TestClaudeStreamCarriesAMidTurnMessage(t *testing.T) {
	path := writeTranscript(t, `{"type":"user","uuid":"u1","timestamp":"2026-10-23T08:29:00Z","origin":{"kind":"human"},"message":{"content":"Run the build"}}`)
	session := seededClaudeStreamSession(t, path, "")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString(`{"type":"assistant","uuid":"a1","timestamp":"2026-10-23T08:29:10Z","message":{"id":"m1","content":[{"type":"text","text":"building"}]}}
{"type":"attachment","uuid":"q1","timestamp":"2026-10-23T08:29:27Z","attachment":{"type":"queued_command","prompt":"store it in the task","source_uuid":"cmd-1","delivery_id":"delivery-1","commandMode":"prompt","origin":{"kind":"human"}}}
`)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	var got []surface.StreamEvent
	err = NewClaude(nil, t.TempDir()).Stream(context.Background(), session, "", func(event surface.StreamEvent) {
		if event.Text == "store it in the task" {
			got = append(got, event)
		}
	}, 150*time.Millisecond)
	if !errors.Is(err, surface.ErrStreamWindow) {
		t.Fatalf("stream err=%v", err)
	}
	if len(got) != 1 || got[0].Kind != "message" || got[0].Role != "user" || got[0].Origin != "" || got[0].TurnID != "u1" || !got[0].Timestamp.Equal(time.Date(2026, 10, 23, 8, 29, 27, 0, time.UTC)) {
		t.Fatalf("events=%+v", got)
	}
}
