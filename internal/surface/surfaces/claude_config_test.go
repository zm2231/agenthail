package surfaces

import (
	"context"
	"fmt"
	"github.com/zm2231/agenthail/internal/signin"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeClaudeSessionRecord(t *testing.T, dir, id string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	record := `{"sessionId":"` + id + `","bridgeSessionId":"session_` + id + `","pid":` + fmt.Sprint(os.Getpid()) + `,"status":"idle"}`
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(record), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeListReadsSessionsFromClaudeConfigDir(t *testing.T) {
	home := t.TempDir()
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	writeClaudeSessionRecord(t, filepath.Join(config, "sessions"), "configured")
	writeClaudeSessionRecord(t, filepath.Join(home, ".claude", "sessions"), "home")
	sessions, err := NewClaude(nil, home).List(context.Background())
	if err != nil || len(sessions) != 1 || sessions[0].ID != "configured" {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
}

func TestClaudeListReadsHomeClaudeWhenConfigDirIsUnset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	writeClaudeSessionRecord(t, filepath.Join(home, ".claude", "sessions"), "home")
	sessions, err := NewClaude(nil, home).List(context.Background())
	if err != nil || len(sessions) != 1 || sessions[0].ID != "home" {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
}

func TestClaudeHealthWithoutRemoteControlNeedsNoSidecar(t *testing.T) {
	t.Setenv("AGENTHAIL_SIDECAR", "")
	t.Setenv("PATH", t.TempDir())
	if err := NewClaude(nil, t.TempDir()).Health(context.Background()); err != nil {
		t.Fatalf("health with no sign-in profile and no Remote Control session err=%v", err)
	}
	claude := NewClaude(signin.Fixed{Browser: signin.Chrome}, t.TempDir())
	if err := claude.Health(context.Background()); err == nil || !strings.Contains(err.Error(), "sidecar not found") {
		t.Fatalf("remote control health err=%v, want sidecar not found", err)
	}
	claude.DisableRemoteControl()
	if err := claude.Health(context.Background()); err != nil {
		t.Fatalf("local-only health with no sessions err=%v", err)
	}
}
