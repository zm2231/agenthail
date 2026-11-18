package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/agenttools"
)

func runSubagentHook(t *testing.T, input string, args ...string) map[string]any {
	t.Helper()
	stdin, err := os.CreateTemp(t.TempDir(), "hook-input")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.WriteString(input); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	previous := os.Stdin
	os.Stdin = stdin
	defer func() { os.Stdin = previous }()
	output, err := captureStdout(t, func() error { return (&App{}).Run(append([]string{"hook", "subagent"}, args...)) })
	if err != nil {
		t.Fatal(err)
	}
	if output == "" {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("hook output %q: %v", output, err)
	}
	return decoded["hookSpecificOutput"].(map[string]any)
}

func subagentToolCall(agentID, tool string) string {
	input, _ := json.Marshal(map[string]any{"session_id": "parent", "hook_event_name": "PreToolUse", "agent_id": agentID, "tool_name": tool, "tool_input": map[string]any{}})
	return string(input)
}

func TestASubagentIsRefusedEveryAgenthailToolThatActsInItsSessionsName(t *testing.T) {
	t.Setenv(claudeHostedEnv, "")
	acting := 0
	for _, tool := range agenttools.Tools {
		output := runSubagentHook(t, subagentToolCall("worker-fork", "mcp__agenthail__"+tool.Name))
		if tool.Subagents {
			if output != nil {
				t.Errorf("%s, which only reads or works on this host, was refused in a subagent: %v", tool.Name, output)
			}
			continue
		}
		acting++
		if output["permissionDecision"] != "deny" || !strings.Contains(output["permissionDecisionReason"].(string), "final message") {
			t.Errorf("%s acts in the session's name but a subagent may call it: %v", tool.Name, output)
		}
	}
	for _, tool := range []string{agenttools.EndTurn, "agenthail_send", "agenthail_report", agenttools.Ask, "agenthail_answer"} {
		if agenttools.ForSubagents(tool) {
			t.Errorf("%s is open to subagents", tool)
		}
	}
	if acting == 0 {
		t.Fatal("no tool acts in the session's name")
	}
	if output := runSubagentHook(t, subagentToolCall("", "mcp__agenthail__"+agenttools.EndTurn)); output != nil {
		t.Fatalf("the session's own end of turn was refused: %v", output)
	}
	if output := runSubagentHook(t, subagentToolCall("worker-fork", "mcp__other__agenthail_send")); output != nil {
		t.Fatalf("another server's tool was refused: %v", output)
	}
}

func TestASubagentIsToldItsResultGoesBackToTheSessionItRunsIn(t *testing.T) {
	t.Setenv(claudeHostedEnv, "")
	output := runSubagentHook(t, `{"session_id":"parent","hook_event_name":"SubagentStart","agent_id":"worker-fork","agent_type":"fork"}`)
	if output["hookEventName"] != "SubagentStart" || !strings.Contains(output["additionalContext"].(string), "Your final message is your result") {
		t.Fatalf("subagent start: %v", output)
	}
}

func TestTheInstalledSubagentHookLeavesAHostedClaudeToItsHost(t *testing.T) {
	t.Setenv(claudeHostedEnv, "1")
	call := subagentToolCall("worker-fork", "mcp__agenthail__"+agenttools.EndTurn)
	if output := runSubagentHook(t, call); output != nil {
		t.Fatalf("the hook from the person's settings answered inside a hosted Claude: %v", output)
	}
	if output := runSubagentHook(t, call, "--host"); output["permissionDecision"] != "deny" {
		t.Fatalf("the host's own hook: %v", output)
	}
}

func TestOnlyTheSubagentHookTakesHost(t *testing.T) {
	if err := validateCommandFlags("hook", []string{"subagent", "--host"}); err != nil {
		t.Fatalf("hook subagent --host: %v", err)
	}
	if err := validateCommandFlags("hook", []string{"permission-request", "--host"}); err == nil {
		t.Fatal("hook permission-request accepted --host")
	}
}
