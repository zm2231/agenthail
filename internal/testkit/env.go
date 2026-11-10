package testkit

import (
	"os"
	"strings"
)

func clearAgentEnv() {
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		switch {
		case name == "CLAUDE_CONFIG_DIR", name == "CLAUDECODE", strings.HasPrefix(name, "CLAUDE_CODE_"),
			name == "AGENTHAIL_SESSION_ID", name == "CODEX_THREAD_ID":
			os.Unsetenv(name)
		}
	}
}
