package surfaces

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeClaudeSessionRecord(t *testing.T, dir, id string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	record := `{"bridgeSessionId":"` + id + `","pid":` + fmt.Sprint(os.Getpid()) + `,"status":"idle"}`
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
	sessions, err := NewClaude("", home).List(context.Background())
	if err != nil || len(sessions) != 1 || sessions[0].ID != "configured" {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
}

func TestClaudeListReadsHomeClaudeWhenConfigDirIsUnset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	writeClaudeSessionRecord(t, filepath.Join(home, ".claude", "sessions"), "home")
	sessions, err := NewClaude("", home).List(context.Background())
	if err != nil || len(sessions) != 1 || sessions[0].ID != "home" {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
}
