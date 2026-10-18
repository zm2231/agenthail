package cli

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "AGENTHAIL_SESSION_ID", "CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID"} {
		os.Unsetenv(key)
	}
	os.Exit(m.Run())
}
