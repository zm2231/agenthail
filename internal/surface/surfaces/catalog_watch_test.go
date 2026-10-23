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

func TestCodexWatchesTheNewestDayAndEveryFolderAboveIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	sessions := filepath.Join(home, "sessions")
	for _, day := range []string{"2025/12/31", "2026/10/22", "2026/10/23", "2026/09/30"} {
		if err := os.MkdirAll(filepath.Join(sessions, day), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sessions, "2026", "10", "zz-not-a-folder"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	dirs := func() string { return strings.Join(NewCodex("").CatalogWatchDirs(), ",") }
	want := strings.Join([]string{sessions, filepath.Join(sessions, "2026"), filepath.Join(sessions, "2026", "10"), filepath.Join(sessions, "2026", "10", "23")}, ",")
	if got := dirs(); got != want {
		t.Fatalf("dirs=%s want %s", got, want)
	}
	if err := os.MkdirAll(filepath.Join(sessions, "2026", "10", "24"), 0700); err != nil {
		t.Fatal(err)
	}
	if got := dirs(); !strings.HasSuffix(got, filepath.Join("2026", "10", "24")) {
		t.Fatalf("a new day is not watched: %s", got)
	}
	t.Setenv("CODEX_HOME", filepath.Join(home, "fresh"))
	if got := dirs(); got != filepath.Join(home, "fresh", "sessions") {
		t.Fatalf("a Codex home with no sessions yet: %s", got)
	}
}
