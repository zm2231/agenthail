package surfaces

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

// claudeGoalSequence is what Claude Code 2.1.304 writes when /goal runs in a
// session: the caveat, the invocation, its output, the Stop hook instruction
// that starts the model's turn, the reply, and the turn's end.
const claudeGoalSequence = `{"type":"user","uuid":"u-human","message":{"role":"user","content":"Reply with exactly: ready"},"timestamp":"2026-10-20T18:24:06Z"}
{"type":"assistant","uuid":"a-1","message":{"id":"m-1","role":"assistant","content":[{"type":"text","text":"ready"}],"stop_reason":"end_turn"},"timestamp":"2026-10-20T18:24:07Z"}
{"type":"system","subtype":"turn_duration","durationMs":1000,"timestamp":"2026-10-20T18:24:08Z"}
{"type":"user","uuid":"u-caveat","isMeta":true,"message":{"role":"user","content":"<local-command-caveat>The command below was run directly in Claude Code, not sent to you as a request, and its output goes straight to the user. It's recorded here as context for later messages.</local-command-caveat>"},"timestamp":"2026-10-20T18:30:23Z"}
{"type":"user","uuid":"u-command","message":{"role":"user","content":"<command-name>/goal</command-name>\n            <command-message>goal</command-message>\n            <command-args>you have replied with exactly the word READY</command-args>"},"timestamp":"2026-10-20T18:30:23Z"}
{"type":"system","subtype":"local_command","uuid":"s-output","isMeta":false,"content":"<local-command-stdout>Goal set: you have replied with exactly the word READY</local-command-stdout>","commandRun":{"command":"goal","args":"you have replied with exactly the word READY"},"timestamp":"2026-10-20T18:30:23Z"}
{"type":"user","uuid":"u-hook","isMeta":true,"message":{"role":"user","content":"A session-scoped Stop hook is now active with condition: \"you have replied with exactly the word READY\". Briefly acknowledge the goal, then immediately start (or continue) working toward it."},"timestamp":"2026-10-20T18:30:23Z"}
{"type":"assistant","uuid":"a-2","message":{"id":"m-2","role":"assistant","content":[{"type":"text","text":"READY"}],"stop_reason":"end_turn"},"timestamp":"2026-10-20T18:30:27Z"}
{"type":"system","subtype":"turn_duration","durationMs":4000,"timestamp":"2026-10-20T18:30:28Z"}
`

func TestClaudeGoalCommandRendersAsEventsNotUserMessages(t *testing.T) {
	page, err := readTimeline(context.Background(), timelineFixture(t, claudeGoalSequence), "claude", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var users, events []surface.TimelineItem
	var reply surface.TimelineItem
	for _, item := range page.Items {
		switch {
		case item.Kind == "message" && item.Role == "user":
			users = append(users, item)
		case item.Kind == "event" && item.Title != "Turn duration":
			events = append(events, item)
		case item.Kind == "message" && item.Text == "READY":
			reply = item
		}
	}
	if len(users) != 1 || users[0].Text != "Reply with exactly: ready" {
		t.Fatalf("only the typed prompt is a user message: %+v", users)
	}
	if len(events) != 3 ||
		events[0].Title != "Command" || events[0].Text != "/goal you have replied with exactly the word READY" || events[0].Origin != "" ||
		events[1].Title != "Local command" || events[1].Text != "Goal set: you have replied with exactly the word READY" ||
		events[2].Title != "Claude Code instructions" || events[2].Text != "" || events[2].Origin != "" {
		t.Fatalf("events=%+v", events)
	}
	for _, item := range page.Items {
		if strings.Contains(item.Text, "<command-") || strings.Contains(item.Text, "<local-command") {
			t.Fatalf("command markup leaked into %+v", item)
		}
	}
	if reply.TurnID != "u-hook" {
		t.Fatalf("the Stop hook instruction starts the model's turn: reply=%+v", reply)
	}
	if events[0].TurnID == "u-command" || events[1].TurnID == "u-command" {
		t.Fatalf("a local command does not start a turn: %+v", events)
	}
}

func TestClaudeGoalCommandIsNotAnExchange(t *testing.T) {
	read, err := timelineClaude().ReadSession(context.Background(), &surface.Session{Transcript: timelineFixture(t, claudeGoalSequence), Status: surface.StatusIdle}, surface.SessionReadRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, exchange := range read.Exchanges {
		if strings.Contains(exchange.User, "<") || strings.Contains(exchange.User, "Stop hook") || exchange.Origin == claudeOriginCommand {
			t.Fatalf("exchanges=%+v", read.Exchanges)
		}
	}
	if len(read.Exchanges) == 0 || read.Exchanges[0].User != "Reply with exactly: ready" {
		t.Fatalf("exchanges=%+v", read.Exchanges)
	}
}

func TestClaudeCommandMarkupClassifies(t *testing.T) {
	user := func(content any, meta bool) map[string]any {
		record := map[string]any{"type": "user", "uuid": "u", "message": map[string]any{"role": "user", "content": content}}
		if meta {
			record["isMeta"] = true
		}
		return record
	}
	typed := func(text string) map[string]any {
		record := user(text, false)
		record["origin"] = map[string]any{"kind": claudeOriginHuman}
		return record
	}
	cases := []struct {
		name   string
		record map[string]any
		want   []surface.TimelineItem
	}{
		{"model command", user("<command-name>/model</command-name>\n<command-message>model</command-message>\n<command-args>haiku</command-args>", false), []surface.TimelineItem{{Kind: "event", Title: "Command", Text: "/model haiku"}}},
		{"compact without args", user("<command-name>/compact</command-name>\n<command-message>compact</command-message>\n<command-args></command-args>", false), []surface.TimelineItem{{Kind: "event", Title: "Command", Text: "/compact"}}},
		{"stdout written as a user record", user("<local-command-stdout>Set model to \x1b[1mHaiku\x1b[22m</local-command-stdout>", false), []surface.TimelineItem{{Kind: "event", Title: "Local command", Text: "Set model to Haiku"}}},
		{"stderr", user([]any{map[string]any{"type": "text", "text": "<local-command-stderr>Unknown model</local-command-stderr>"}}, false), []surface.TimelineItem{{Kind: "event", Title: "Local command", Text: "Unknown model", Status: "error"}}},
		{"caveat", user("<local-command-caveat>The command below was run directly in Claude Code.</local-command-caveat>", true), nil},
		{"system output", map[string]any{"type": "system", "subtype": "local_command", "content": "<local-command-stdout>Compacted</local-command-stdout>"}, []surface.TimelineItem{{Kind: "event", Title: "Local command", Text: "Compacted"}}},
		{"meta instruction", user("Stop hook feedback:\n[condition]: not met yet", true), []surface.TimelineItem{{Kind: "event", Title: "Claude Code instructions"}}},
		{"meta skill body", user([]any{map[string]any{"type": "text", "text": "Base directory for this skill: /x"}}, true), []surface.TimelineItem{{Kind: "event", Title: "Claude Code instructions"}}},
		{"meta peer message without origin", user("Another Claude session sent a message:\n<cross-session-message from=\"uds:/tmp/x.sock\" from-name=\"operator\">\nReply with exactly: second\n</cross-session-message>", true), []surface.TimelineItem{{Kind: "message", Role: "peer", Origin: claudeOriginPeer, Sender: "operator", Title: "operator", Text: "Reply with exactly: second"}}},
		{"typed prompt", user("fix the build", false), []surface.TimelineItem{{Kind: "message", Role: "user", Title: "user", Text: "fix the build"}}},
		{"slash command the person typed", typed("<command-message>goal</command-message>\n<command-name>/goal</command-name>\n<command-args>tests pass</command-args>"), []surface.TimelineItem{{Kind: "message", Role: "user", Title: "user", Text: "/goal tests pass"}}},
		{"typed text that quotes command output", typed("<local-command-stdout>Goal set</local-command-stdout>"), []surface.TimelineItem{{Kind: "message", Role: "user", Title: "user", Text: "<local-command-stdout>Goal set</local-command-stdout>", Origin: claudeOriginCommand}}},
		{"markup followed by prose", user("<command-name>example</command-name> what does this tag mean?", false), []surface.TimelineItem{{Kind: "message", Role: "user", Title: "user", Text: "<command-name>example</command-name> what does this tag mean?"}}},
	}
	for _, tc := range cases {
		got := claudeTimelineItems(tc.record)
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: got %+v want %+v", tc.name, got, tc.want)
		}
	}
	for _, text := range []string{"<command-name>/goal</command-name>", "<local-command-stdout>x</local-command-stdout>", "<local-command-caveat>x</local-command-caveat>"} {
		if isHumanTranscriptText(text) || !claudeRecordIsCommand(user(text, false)) || claudeRecordStartsTurn(user(text, false)) {
			t.Fatalf("%q is command markup, not a human turn", text)
		}
	}
	if !claudeRecordStartsTurn(typed("<command-name>/goal</command-name><command-args>tests pass</command-args>")) {
		t.Fatal("a slash command the person typed starts their turn")
	}
	if !claudeRecordStartsTurn(user("<command-name>example</command-name> what does this tag mean?", false)) || !isHumanTranscriptText("<command-name>example</command-name> what does this tag mean?") {
		t.Fatal("a prompt that only begins with command markup is the person's")
	}
	if !claudeRecordStartsTurn(user("A session-scoped Stop hook is now active", true)) {
		t.Fatal("a meta instruction still starts the model's turn")
	}
}
