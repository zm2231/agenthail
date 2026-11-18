package claudehooks

import (
	"strings"

	"github.com/zm2231/agenthail/internal/agenttools"
	"github.com/zm2231/agenthail/internal/claudemcp"
)

// SubagentNote is what a Claude subagent is told as it starts. A forked
// subagent inherits its parent's context, Agenthail's note about the parent
// included, and reads that note as its own.
const SubagentNote = "[Agenthail] You are a subagent working inside another agent's session, not an Agenthail session of your own. " +
	"Your final message is your result: it goes back to the agent that started you, which decides what to do with it. " +
	"Don't message @lead, the person or other sessions, and don't end the turn, report, ask or answer through Agenthail: " +
	"those tools would act in that agent's name, so they are refused here. Reading sessions, the ledger and requests still works. " +
	"An earlier \"[Agenthail] You are @…\" note in your context describes that agent, not you."

const subagentRefusal = "Agenthail refuses this tool in a subagent: it would act in the name of the session you run inside. " +
	"Put what you would have sent in your final message; the agent that started you gets it and can act on it."

// SubagentOutput is the hook output for a Claude hook that fires inside a
// subagent, or nil when Agenthail has nothing to add: agentID is empty on the
// main thread.
func SubagentOutput(event, agentID, toolName string) map[string]any {
	if agentID == "" {
		return nil
	}
	switch event {
	case SubagentStart.Event:
		return map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": event, "additionalContext": SubagentNote}}
	case SubagentTool.Event:
		tool, ours := strings.CutPrefix(toolName, "mcp__"+claudemcp.Name+"__")
		if !ours || agenttools.ForSubagents(tool) {
			return nil
		}
		return map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": event, "permissionDecision": "deny", "permissionDecisionReason": subagentRefusal}}
	}
	return nil
}
