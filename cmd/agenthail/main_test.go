package main

import "testing"

func TestCodexRemotePortRequiresExplicitOverride(t *testing.T) {
	t.Setenv("AGENTHAIL_CODEX_REMOTE", "")
	t.Setenv("AGENTHAIL_CODEX_INSPECT", "")
	if got := codexRemotePort(); got != "" {
		t.Fatalf("codexRemotePort() = %q, want empty managed-runtime default", got)
	}
	t.Setenv("AGENTHAIL_CODEX_REMOTE", "9231")
	if got := codexRemotePort(); got != "9231" {
		t.Fatalf("codexRemotePort() = %q, want explicit remote override", got)
	}
}
