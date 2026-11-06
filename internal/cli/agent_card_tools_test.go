package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestASessionWithAgenthailsToolsIsTaughtTheToolsAndATerminalOneTheCLI(t *testing.T) {
	app, _, _ := cardFixture(t)

	terminal := helpAgentAs(t, app, "planner-conversation")
	for _, want := range []string{`agenthail send <@alias|session-id> "<message>"`, "agenthail thread create", "agenthail whoami --json always has the current answer"} {
		if !strings.Contains(terminal, want) {
			t.Fatalf("the terminal card does not say %q:\n%s", want, terminal)
		}
	}
	if strings.Contains(terminal, "agenthail_") {
		t.Fatalf("the terminal card names tools it does not have:\n%s", terminal)
	}

	if _, err := app.Registry.IssueSessionCredential("planner-conversation", time.Now()); err != nil {
		t.Fatal(err)
	}
	hosted := helpAgentAs(t, app, "planner-conversation")
	for _, want := range []string{
		`agenthail_send {"target":"<@alias|session-id>","message":"<message>"}`,
		`agenthail_create {"surface":"claude|codex"`,
		`agenthail_wait {"tasks":["<taskId>"]}`,
		`agenthail_channel_send {"channel":"#project-card"`,
		"agenthail_whoami always has the current answer",
	} {
		if !strings.Contains(hosted, want) {
			t.Fatalf("the hosted card does not say %q:\n%s", want, hosted)
		}
	}
	if strings.Contains(hosted, "agenthail send") || strings.Contains(hosted, "agenthail thread create") {
		t.Fatalf("the hosted card still teaches the CLI:\n%s", hosted)
	}

	var pinned struct {
		CLICardBytes int `json:"cliCardBytes"`
		MCPCardBytes int `json:"mcpCardBytes"`
	}
	data, err := os.ReadFile("../agenttools/budget.json")
	if err != nil || json.Unmarshal(data, &pinned) != nil {
		t.Fatalf("read budget: %v", err)
	}
	if len(terminal) != pinned.CLICardBytes || len(hosted) != pinned.MCPCardBytes {
		t.Fatalf("the cards changed size; if that is intended, set cliCardBytes to %d and mcpCardBytes to %d in internal/agenttools/budget.json (pinned %d and %d)", len(terminal), len(hosted), pinned.CLICardBytes, pinned.MCPCardBytes)
	}
}

func TestAHostedLeadIsToldToReadAndAnswerPromptsWithTheTools(t *testing.T) {
	app, _, _ := cardFixture(t)
	if _, err := app.Registry.IssueSessionCredential("lead-conversation", time.Now()); err != nil {
		t.Fatal(err)
	}
	text := helpAgentAs(t, app, "lead-conversation")
	if !strings.Contains(text, `read the request with agenthail_requests {"sessionId":"<target>"} (answer with agenthail_answer)`) || strings.Contains(text, "agenthail requests") {
		t.Fatalf("the hosted lead is not told to use the tools for prompts:\n%s", text)
	}
}
