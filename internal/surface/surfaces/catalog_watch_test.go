package surfaces

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeWatchesEveryConfigRootForNewSessionRecords(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	second := filepath.Join(home, "claude-work")
	if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".agenthail", "config.json"), []byte(`{"claudeConfigDirs":["`+second+`"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(NewClaude(nil, home).CatalogWatchDirs(), ",")
	if want := filepath.Join(home, ".claude", "sessions") + "," + filepath.Join(second, "sessions"); got != want {
		t.Fatalf("dirs=%s want %s", got, want)
	}
}
