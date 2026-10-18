package surface

import (
	"slices"
	"testing"
)

func TestAgentEnvDropsTheLaunchersCallerIdentity(t *testing.T) {
	env := AgentEnv([]string{"PATH=/bin", "CLAUDE_CODE_SESSION_ID=parent", "CLAUDE_CODE_BRIDGE_SESSION_ID=session_parent", "CLAUDE_CODE_MESSAGING_SOCKET=/tmp/parent.sock", "CLAUDE_CODE_MESSAGING_TOKEN=secret", "CODEX_THREAD_ID=thread", "AGENTHAIL_SESSION_ID=pinned", "CLAUDE_CODE_SESSION_ID_EXTRA=kept"})
	if !slices.Equal(env, []string{"PATH=/bin", "CLAUDE_CODE_SESSION_ID_EXTRA=kept"}) {
		t.Fatalf("env=%v", env)
	}
}
