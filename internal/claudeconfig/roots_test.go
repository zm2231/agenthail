package claudeconfig

import (
	"os"
	"path/filepath"
	"strings"
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

func TestRootsLoadsSharedSavedConfiguration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("AGENTHAIL_CLAUDE_CONFIG_DIRS", "")
	dir := filepath.Join(home, ".agenthail")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"claudeConfigDirs":["/fixture/gentle/.claude"],"otherSetting":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	roots, err := Roots(home)
	if err != nil || len(roots) != 2 || roots[1] != "/fixture/gentle/.claude" {
		t.Fatalf("roots=%v err=%v", roots, err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_CONFIG_DIRS", "/fixture/gentle/.claude"+string(os.PathListSeparator)+"/fixture/another/.claude")
	roots, err = Roots(home)
	if err != nil || len(roots) != 3 || roots[2] != "/fixture/another/.claude" {
		t.Fatalf("roots=%v err=%v", roots, err)
	}
}

func TestRootsRejectsInvalidSavedConfiguration(t *testing.T) {
	for _, test := range []struct{ name, content string }{
		{"malformed", "{"},
		{"null", "null"},
		{"array", "[]"},
		{"wrong type", `{"claudeConfigDirs":"/fixture"}`},
		{"relative root", `{"claudeConfigDirs":["relative"]}`},
		{"oversized", strings.Repeat(" ", 64*1024+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			t.Setenv("AGENTHAIL_CLAUDE_CONFIG_DIRS", "")
			dir := filepath.Join(home, ".agenthail")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(test.content), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Roots(home); err == nil {
				t.Fatal("invalid saved config accepted")
			}
		})
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
