package testkit

import (
	"os"
	"strings"
	"testing"
)

// IsolateAgentEnv clears, for the rest of the test, the Claude configuration
// directory and every variable an agent session sets for its children. With
// them, a fixture reads and writes the real configuration directory and takes
// the enclosing session as its caller.
func IsolateAgentEnv(t testing.TB) {
	t.Helper()
	for _, name := range agentSessionVariables() {
		t.Setenv(name, "")
	}
}

func ClearAgentEnv() {
	for _, name := range agentSessionVariables() {
		os.Unsetenv(name)
	}
}

func agentSessionVariables() []string {
	var names []string
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		switch {
		case name == "CLAUDE_CONFIG_DIR", name == "CLAUDECODE", strings.HasPrefix(name, "CLAUDE_CODE_"),
			name == "AGENTHAIL_SESSION_ID", name == "CODEX_THREAD_ID":
			names = append(names, name)
		}
	}
	return names
}
