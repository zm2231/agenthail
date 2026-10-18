package surface

import "strings"

// callerIdentityVariables name the session a process's commands act for.
// Claude and Codex set them for their own tool commands; an agent Agenthail
// starts must not inherit its launcher's, or its commands would be
// attributed to the launcher's conversation.
var callerIdentityVariables = []string{
	"AGENTHAIL_SESSION_ID",
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDE_CODE_BRIDGE_SESSION_ID",
	"CLAUDE_CODE_MESSAGING_SOCKET",
	"CLAUDE_CODE_MESSAGING_TOKEN",
	"CODEX_THREAD_ID",
}

// AgentEnv returns env without the caller identity variables.
func AgentEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		inherited := false
		for _, variable := range callerIdentityVariables {
			inherited = inherited || name == variable
		}
		if !inherited {
			out = append(out, entry)
		}
	}
	return out
}
