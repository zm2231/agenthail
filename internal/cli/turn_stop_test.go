package cli

import (
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
	for _, extra := range []string{`,"stop_hook_active":true`, `,"agent_id":"sub-1"`} {
		if output := stop(extra); output != nil {
			t.Fatalf("stop with %s was blocked: %v", extra, output)
		}
	}

	end, err := app.Registry.EndTurn(registry.TurnEnd{SessionID: id, Needs: "nothing", Headline: "Done", Source: registry.TurnSourceTool}, "turn-1", time.Now())
	if err != nil || end.Source != registry.TurnSourceNudged {
		t.Fatalf("end %+v err=%v", end, err)
	}
	if output := stop(""); output != nil {
		t.Fatalf("a turn that has ended was asked again: %v", output)
	}
}
