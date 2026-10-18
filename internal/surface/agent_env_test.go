package surface

import (
	"os/exec"
	"slices"
	"testing"
)

func TestAgentEnvDropsTheLaunchersCallerIdentity(t *testing.T) {
	env := AgentEnv([]string{"PATH=/bin", "CLAUDE_CODE_SESSION_ID=parent", "CLAUDE_CODE_BRIDGE_SESSION_ID=session_parent", "CLAUDE_CODE_MESSAGING_SOCKET=/tmp/parent.sock", "CLAUDE_CODE_MESSAGING_TOKEN=secret", "CODEX_THREAD_ID=thread", "AGENTHAIL_SESSION_ID=pinned", "CLAUDE_CODE_SESSION_ID_EXTRA=kept"})
	if !slices.Equal(env, []string{"PATH=/bin", "CLAUDE_CODE_SESSION_ID_EXTRA=kept"}) {
		t.Fatalf("env=%v", env)
	}
}

func TestAgentEnvArgvClearsCallerIdentityUnderAnotherEnvironment(t *testing.T) {
	argv := AgentEnvArgv([]string{"/bin/sh", "-c", `printf '%s|%s|%s|%s' "$AGENTHAIL_SESSION_ID" "$CLAUDE_CODE_SESSION_ID" "$CODEX_THREAD_ID" "$KEEP"`})
	command := exec.Command(argv[0], argv[1:]...)
	command.Env = []string{"PATH=/usr/bin:/bin", "AGENTHAIL_SESSION_ID=launcher", "CLAUDE_CODE_SESSION_ID=launcher-conversation", "CODEX_THREAD_ID=launcher-thread", "KEEP=kept"}
	out, err := command.Output()
	if err != nil || string(out) != "|||kept" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}
