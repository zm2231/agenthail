package codexconfig

import "testing"

func TestRemoteDebuggingPort(t *testing.T) {
	t.Setenv("AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT", "")
	if got := RemoteDebuggingPort(); got != "" {
		t.Fatalf("RemoteDebuggingPort() = %q, want empty", got)
	}
	if got := LaunchRemoteDebuggingPort(); got != DefaultLaunchRemoteDebuggingPort {
		t.Fatalf("LaunchRemoteDebuggingPort() = %q, want %q", got, DefaultLaunchRemoteDebuggingPort)
	}
	t.Setenv("AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT", "9345")
	if got := RemoteDebuggingPort(); got != "9345" {
		t.Fatalf("RemoteDebuggingPort() = %q, want override", got)
	}
	if got := LaunchRemoteDebuggingPort(); got != "9345" {
		t.Fatalf("LaunchRemoteDebuggingPort() = %q, want override", got)
	}
}
