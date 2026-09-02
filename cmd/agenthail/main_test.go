package main

import "testing"

func TestCodexRemotePortRequiresExplicitOverride(t *testing.T) {
	t.Setenv("AGENTHAIL_CODEX_INSPECT", "")
	if got := codexRemotePort(); got != "" {
		t.Fatalf("codexRemotePort() = %q, want empty managed-runtime default", got)
	}
	t.Setenv("AGENTHAIL_CODEX_INSPECT", "9230")
	if got := codexRemotePort(); got != "9230" {
		t.Fatalf("codexRemotePort() = %q, want explicit inspector override", got)
	}
}
