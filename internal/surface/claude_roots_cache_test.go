package surface

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestARootsEditInTheTickOfTheLastReadIsRead(t *testing.T) {
	home := claudeRootsHome(t, "a")
	if err := os.MkdirAll(filepath.Join(home, "b"), 0o700); err != nil {
		t.Fatal(err)
	}
	before, err := ClaudeConfigRoots(home)
	if err != nil || !slices.Contains(before, filepath.Join(home, "a")) {
		t.Fatalf("roots=%v err=%v", before, err)
	}
	info, err := os.Stat(AgenthailConfigPath(home))
	if err != nil {
		t.Fatal(err)
	}
	writeClaudeGlobalConfigFixture(t, AgenthailConfigPath(home), map[string]any{"claudeConfigDirs": []string{filepath.Join(home, "b")}})
	if err := os.Chtimes(AgenthailConfigPath(home), info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, err := ClaudeConfigRoots(home)
	if err != nil || slices.Contains(after, filepath.Join(home, "a")) || !slices.Contains(after, filepath.Join(home, "b")) {
		t.Fatalf("a same-size edit in the tick of the last read was not read: %v err=%v", after, err)
	}
}

func TestAConfiguredRootThatBecomesALinkToThePrimaryIsListedOnce(t *testing.T) {
	home := claudeRootsHome(t, "other")
	primary := ClaudeConfigDir(home)
	if err := os.MkdirAll(primary, 0o700); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(home, "other")
	settled := time.Now().Add(-time.Hour)
	if err := os.Chtimes(AgenthailConfigPath(home), settled, settled); err != nil {
		t.Fatal(err)
	}
	if roots, err := ClaudeConfigRoots(home); err != nil || len(roots) != 2 {
		t.Fatalf("roots=%v err=%v", roots, err)
	}
	if err := os.Remove(other); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(primary, other); err != nil {
		t.Fatal(err)
	}
	if roots, err := ClaudeConfigRoots(home); err != nil || len(roots) != 1 || roots[0] != primary {
		t.Fatalf("a root relinked to the primary is still listed: %v err=%v", roots, err)
	}
}

func TestARootThatTurnsUnreadableAfterItWasMissingIsAnError(t *testing.T) {
	home := claudeRootsHome(t)
	locked := filepath.Join(home, "locked")
	if err := os.MkdirAll(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	writeClaudeGlobalConfigFixture(t, AgenthailConfigPath(home), map[string]any{"claudeConfigDirs": []string{filepath.Join(locked, "root")}})
	settled := time.Now().Add(-time.Hour)
	if err := os.Chtimes(AgenthailConfigPath(home), settled, settled); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaudeConfigRoots(home); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o700) })
	if _, err := os.Stat(filepath.Join(locked, "root")); os.IsNotExist(err) || err == nil {
		t.Skip("permissions are not enforced for this user")
	}
	if roots, err := ClaudeConfigRoots(home); err == nil {
		t.Fatalf("an unreadable root still lists from the cache: %v", roots)
	}
}

func TestADanglingRootLinkWhoseTargetBecomesThePrimaryIsListedOnce(t *testing.T) {
	home := claudeRootsHome(t)
	primary := ClaudeConfigDir(home)
	if err := os.MkdirAll(primary, 0o700); err != nil {
		t.Fatal(err)
	}
	alias, target := filepath.Join(home, "alias"), filepath.Join(home, "target")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	writeClaudeGlobalConfigFixture(t, AgenthailConfigPath(home), map[string]any{"claudeConfigDirs": []string{alias}})
	settled := time.Now().Add(-time.Hour)
	if err := os.Chtimes(AgenthailConfigPath(home), settled, settled); err != nil {
		t.Fatal(err)
	}
	if roots, err := ClaudeConfigRoots(home); err != nil || len(roots) != 2 {
		t.Fatalf("roots=%v err=%v", roots, err)
	}
	if err := os.Symlink(primary, target); err != nil {
		t.Fatal(err)
	}
	if roots, err := ClaudeConfigRoots(home); err != nil || len(roots) != 1 {
		t.Fatalf("a root whose link now reaches the primary is still listed: %v err=%v", roots, err)
	}
}

func TestARootRelinkedWhileRootsAreBeingResolvedIsNotCachedAsBefore(t *testing.T) {
	home := claudeRootsHome(t, "other")
	primary := ClaudeConfigDir(home)
	if err := os.MkdirAll(primary, 0o700); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(home, "other")
	settled := time.Now().Add(-time.Hour)
	if err := os.Chtimes(AgenthailConfigPath(home), settled, settled); err != nil {
		t.Fatal(err)
	}
	relinked := false
	cache := &claudeRootsCache{entries: map[string]claudeRootsEntry{}, resolve: func(home string) ([]claudeRootSource, []string, error) {
		sources, candidates, err := resolveClaudeConfigRootSources(home)
		if !relinked {
			relinked = true
			if err := os.Remove(other); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(primary, other); err != nil {
				t.Fatal(err)
			}
		}
		return sources, candidates, err
	}}
	if _, err := cache.sources(home); err != nil {
		t.Fatal(err)
	}
	if sources, err := cache.sources(home); err != nil || len(sources) != 1 {
		t.Fatalf("a root relinked to the primary mid-resolve is still listed: %v err=%v", sources, err)
	}
}
