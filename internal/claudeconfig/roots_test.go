package claudeconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRootsIncludesNativeAndConfiguredDirectories(t *testing.T) {
	home := t.TempDir()
	gentle := filepath.Join(home, "gentle", ".claude")
	t.Setenv("CLAUDE_CONFIG_DIR", gentle)
	t.Setenv("AGENTHAIL_CLAUDE_CONFIG_DIRS", gentle+string(os.PathListSeparator)+filepath.Join(home, "other"))
	roots, err := Roots(home)
	if err != nil || len(roots) != 3 || roots[0] != filepath.Join(home, ".claude") || roots[1] != gentle {
		t.Fatalf("roots=%v err=%v", roots, err)
	}
}

func TestNormalizeDeduplicatesSymlinkAndRejectsRelativeRoot(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	roots, err := Normalize([]string{root, alias})
	if err != nil || len(roots) != 1 {
		t.Fatalf("roots=%v err=%v", roots, err)
	}
	for _, path := range []string{"", "relative/.claude", "~/.claude"} {
		if _, err := Normalize([]string{path}); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
}
