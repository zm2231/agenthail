package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestClaudeConfigRootsDiscoveryReadsAndAmbiguity(t *testing.T) {
	home := t.TempDir()
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	defaultRoot, alternate := filepath.Join(home, ".claude"), filepath.Join(home, "gentle", ".claude")
	write := func(root, id string) {
		t.Helper()
		dir := filepath.Join(root, "sessions")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "sessionId": id, "bridgeSessionId": "session_" + id, "cwd": "/fixture", "name": id})
		if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(os.Getpid())+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "projects", "-fixture", id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		content := `{"type":"user","uuid":"u","message":{"content":"question"}}` + "\n" + `{"type":"assistant","message":{"content":[{"type":"text","text":"` + id + ` answer"}],"stop_reason":"end_turn"}}` + "\n"
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(defaultRoot, "native")
	write(alternate, "gentle")
	adapter, err := NewClaudeWithConfigDirs("", home, []string{defaultRoot, alternate, alternate})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := adapter.List(context.Background())
	if err != nil || len(sessions) != 2 {
		t.Fatalf("sessions=%v err=%v", sessions, err)
	}
	gentle, err := adapter.Resolve(context.Background(), "session_gentle")
	if err != nil || gentle.ConfigDir != alternate {
		t.Fatalf("session=%v err=%v", gentle, err)
	}
	page, err := adapter.ReadSession(context.Background(), gentle, surface.SessionReadRequest{Limit: 1})
	if err != nil || len(page.Exchanges) != 1 || page.Exchanges[0].Assistant != "gentle answer" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	write(alternate, "native")
	if _, err := adapter.List(context.Background()); err == nil || !strings.Contains(err.Error(), "multiple config directories") {
		t.Fatalf("ambiguity err=%v", err)
	}
}

func TestClaudeLifecycleUsesTargetConfigRoot(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess integration")
	}
	home := t.TempDir()
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, "gentle", ".claude")
	binary := filepath.Join(home, "claude")
	script := `#!/bin/sh
printf '%s\n' "$CLAUDE_CONFIG_DIR" >> "$HOME/config-roots"
if [ "$1" = agents ]; then
 printf '[{"id":"bg-one","sessionId":"gentle-session","kind":"background","state":"done"}]\n'
else
 printf 'operation complete\n'
fi
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_BIN", binary)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "wrong-root"))
	adapter, err := NewClaudeWithConfigDirs("", home, []string{filepath.Join(home, ".claude"), root})
	if err != nil {
		t.Fatal(err)
	}
	session := &surface.Session{ID: "gentle-session", ConfigDir: root}
	if _, err := adapter.SessionAction(context.Background(), session, "logs"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "config-roots")
	before, err := os.ReadFile(path)
	if err != nil || string(before) != root+"\n"+root+"\n" {
		t.Fatalf("roots=%q err=%v", before, err)
	}
	session.ConfigDir = filepath.Join(home, "unconfigured")
	if _, err := adapter.SessionAction(context.Background(), session, "logs"); err == nil {
		t.Fatal("unconfigured root accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("unexpected subprocess roots=%q err=%v", after, err)
	}
}
