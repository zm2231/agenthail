package surface

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestASessionMovesOnlyToADirectlySignedInRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	direct, proxied := filepath.Join(home, ".work", ".claude"), filepath.Join(home, ".pool", ".claude")
	writeClaudeGlobalConfigFixture(t, AgenthailConfigPath(home), map[string]any{
		"claudeConfigDirs": []any{direct, map[string]any{"path": proxied, "proxy": "shared"}},
		"proxies":          []any{map[string]any{"name": "shared", "url": "http://127.0.0.1:8317"}},
	})
	if root, err := ResolveClaudeMoveRoot(home, "work"); err != nil || root != direct {
		t.Fatalf("root=%q err=%v, want %s", root, err, direct)
	}
	if _, err := ResolveClaudeMoveRoot(home, "pool"); err == nil || !strings.Contains(err.Error(), "shared proxy") {
		t.Fatalf("a move into a proxied root was allowed: %v", err)
	}
	if _, err := ResolveClaudeMoveRoot(home, "nobody"); err == nil {
		t.Fatal("a move into an unknown root was allowed")
	}
}
