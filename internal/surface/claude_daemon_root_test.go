package surface

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAnUnusedStandardRootIsNotARootOfADaemonRunningElsewhere(t *testing.T) {
	home := t.TempDir()
	persist := filepath.Join(home, "persist", "claude")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", persist)
	if err := RecordDaemonClaudeRoot(home); err != nil {
		t.Fatal(err)
	}
	roots, err := ClaudeConfigRoots(home)
	if err != nil || !reflect.DeepEqual(roots, []string{persist}) {
		t.Fatalf("roots=%v err=%v, want only the daemon's root", roots, err)
	}
}

func TestACallerInAnotherRootStillReadsTheDaemonsRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if err := RecordDaemonClaudeRoot(home); err != nil {
		t.Fatal(err)
	}
	listed := filepath.Join(home, "listed")
	if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(AgenthailConfigPath(home), []byte(`{"claudeConfigDirs":["`+listed+`"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", listed)
	roots, err := ClaudeConfigRoots(home)
	want := []string{listed, filepath.Join(home, ".claude")}
	if err != nil || !reflect.DeepEqual(roots, want) {
		t.Fatalf("roots=%v err=%v, want %v", roots, err, want)
	}
}

func TestAnUnreadableDaemonRootRecordIsAnError(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemonClaudeRootPath(home), []byte("relative/root\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if roots, err := ClaudeConfigRoots(home); err == nil {
		t.Fatalf("roots=%v, want the bad record refused", roots)
	}
}
