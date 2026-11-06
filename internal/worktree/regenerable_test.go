package worktree

import "testing"

func TestOnlyFilesInsideRegenerableFoldersAreDisposable(t *testing.T) {
	for path, want := range map[string]bool{
		"node_modules/":          true,
		"web/node_modules/":      true,
		"build/out.o":            true,
		"app/.venv/lib/x.py":     true,
		"DerivedData/":           true,
		".env":                   false,
		"config/secrets.json":    false,
		"build":                  false,
		"notes/build-log.txt":    false,
		"src/binary/":            false,
		"coverage/lcov.info":     true,
		"crates/x/target/debug/": true,
	} {
		if got := Regenerable(path); got != want {
			t.Errorf("Regenerable(%q)=%t, want %t", path, got, want)
		}
	}
}
