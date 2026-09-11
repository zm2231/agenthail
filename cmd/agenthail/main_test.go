package main

import (
	"testing"

	"github.com/zm2231/agenthail/internal/codexconfig"
)

func TestCodexRemotePortRequiresExplicitOverride(t *testing.T) {
	t.Setenv("AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT", "")
	if got := codexconfig.RemoteDebuggingPort(); got != "" {
		t.Fatalf("RemoteDebuggingPort() = %q, want empty managed-runtime default", got)
	}
	t.Setenv("AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT", "9231")
	if got := codexconfig.RemoteDebuggingPort(); got != "9231" {
		t.Fatalf("RemoteDebuggingPort() = %q, want explicit renderer override", got)
	}
}
