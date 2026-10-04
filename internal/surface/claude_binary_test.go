package surface

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeExecutableSelectionPreservesExplicitChoice(t *testing.T) {
	home := t.TempDir()
	native := filepath.Join(home, ".local", "bin", "claude")
	pathBinary := filepath.Join(t.TempDir(), "claude")
	custom := filepath.Join(t.TempDir(), "custom claude")
	for _, binary := range []string{native, pathBinary, custom} {
		if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", filepath.Dir(pathBinary))
	t.Setenv("AGENTHAIL_CLAUDE_BIN", custom)
	if got, err := ClaudeBinary(home); err != nil || got != custom {
		t.Fatalf("explicit binary=%q err=%v", got, err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_BIN", filepath.Join(home, "missing"))
	if got, err := ClaudeBinary(home); err == nil || got != "" {
		t.Fatalf("invalid explicit binary used another install: %q err=%v", got, err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_BIN", "")
	if got, err := ClaudeBinary(home); err != nil || got != pathBinary {
		t.Fatalf("PATH binary=%q err=%v", got, err)
	}
	t.Setenv("PATH", t.TempDir())
	if got, err := ClaudeBinary(home); err != nil || got != native {
		t.Fatalf("native binary=%q err=%v", got, err)
	}
	if err := os.Chmod(native, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := ClaudeBinary(home); err == nil || got != "" {
		t.Fatalf("non-executable native install accepted: %q err=%v", got, err)
	}
}
