package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/agenttools"
	"github.com/zm2231/agenthail/internal/registry"
)

func TestATurnThatStopsWithoutAnEndIsAskedOnceToEndIt(t *testing.T) {
	app, _, _ := cardFixture(t)
	const id = "planner-conversation"
	stop := func(extra string) map[string]any {
		return app.turnStop(strings.NewReader(`{"session_id":"`+id+`","hook_event_name":"Stop","turn_id":"turn-1"`+extra+`}`), time.Now())
	}
	if err := app.Registry.OpenTurn(id, time.Now()); err != nil {
		t.Fatal(err)
	}
	if output := stop(""); output != nil {
		t.Fatalf("a terminal session without the tools was asked to use them: %v", output)
	}

	if _, err := app.Registry.IssueSessionCredential(id, time.Now()); err != nil {
		t.Fatal(err)
	}
	output := stop("")
	if reason, _ := output["reason"].(string); output["decision"] != "block" || !strings.Contains(reason, agenttools.EndTurn) {
		t.Fatalf("a turn without an end stopped: %v", output)
	}
	if output := stop(`,"agenthail_tools":"absent"`); output != nil {
		t.Fatalf("a conversation the Claude host opened without its agenthail server was asked to use its tools: %v", output)
	}
	if output := stop(`,"agenthail_tools":"attached"`); output["decision"] != "block" {
		t.Fatalf("a conversation the Claude host opened with its agenthail server stopped: %v", output)
	}
	for _, extra := range []string{`,"stop_hook_active":true`, `,"agent_id":"sub-1"`} {
		if output := stop(extra); output != nil {
			t.Fatalf("stop with %s was blocked: %v", extra, output)
		}
	}

	end, err := app.Registry.EndTurn(registry.TurnEnd{SessionID: id, Headline: "Done", Source: registry.TurnSourceTool}, "turn-1", time.Now())
	if err != nil || end.Source != registry.TurnSourceNudged {
		t.Fatalf("end %+v err=%v", end, err)
	}
	if output := stop(""); output != nil {
		t.Fatalf("a turn that has ended was asked again: %v", output)
	}
}

func TestALeadWhoseReplyAsksThePersonIsRemindedOnceToRaiseAnAsk(t *testing.T) {
	app, _, _ := cardFixture(t)
	stop := func(id, reply, extra string) map[string]any {
		encoded, _ := json.Marshal(reply)
		return app.turnStop(strings.NewReader(`{"session_id":"`+id+`","hook_event_name":"Stop","last_assistant_message":`+string(encoded)+extra+`}`), time.Now())
	}
	reason := func(output map[string]any) string {
		text, _ := output["reason"].(string)
		return text
	}
	for _, id := range []string{"lead-conversation", "planner-conversation"} {
		if err := app.Registry.OpenTurn(id, time.Now().Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	const asking = "The fix is ready.\n\n**Should I deploy it now?**\n"
	if output := stop("lead-conversation", asking, ""); output["decision"] != "block" || !strings.Contains(reason(output), "agenthail ask ") {
		t.Fatalf("a lead asking in prose stopped: %v", output)
	}
	if output := stop("lead-conversation", asking, `,"stop_hook_active":true`); output != nil {
		t.Fatalf("the reminder came twice: %v", output)
	}
	for name, reply := range map[string]string{"a statement": "Deployed. The error rate is flat.", "a question in code": "Run this:\n```\nread -p 'continue?'\n```\nDone."} {
		if output := stop("lead-conversation", reply, ""); output != nil {
			t.Fatalf("%s was held: %v", name, output)
		}
	}
	if output := stop("planner-conversation", asking, ""); output != nil {
		t.Fatalf("a session that leads no one was held: %v", output)
	}

	if _, err := app.Registry.IssueSessionCredential("lead-conversation", time.Now()); err != nil {
		t.Fatal(err)
	}
	if output := stop("lead-conversation", asking, ""); !strings.Contains(reason(output), agenttools.EndTurn) || !strings.Contains(reason(output), agenttools.Ask) {
		t.Fatalf("a lead with the tools: %v", output)
	}
	question := registry.AskQuestion{Question: "Deploy now?", Options: []registry.AskOption{{ID: "o0", Label: "Deploy"}}}
	if _, err := app.Registry.RaiseAsk(registry.Ask{SessionID: "lead-conversation", Origin: registry.AskOriginAgent, Title: "Deploy now?", Questions: []registry.AskQuestion{question}, RoutedTo: registry.AskToPerson}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Registry.EndTurn(registry.TurnEnd{SessionID: "lead-conversation", Headline: "Ready", Source: registry.TurnSourceTool}, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if output := stop("lead-conversation", asking, ""); output != nil {
		t.Fatalf("a lead that raised its ask and ended its turn was held: %v", output)
	}
}
