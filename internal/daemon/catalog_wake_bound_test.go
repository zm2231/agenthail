package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/filewake"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func openDescriptors(t *testing.T) int {
	t.Helper()
	dir, err := os.Open("/dev/fd")
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	names, err := dir.Readdirnames(-1)
	if err != nil {
		t.Fatal(err)
	}
	return len(names)
}

func addCodexDay(t *testing.T, sessions string, day int) string {
	t.Helper()
	dir := filepath.Join(sessions, fmt.Sprintf("%04d", 2025+day/336), fmt.Sprintf("%02d", 1+day/28%12), fmt.Sprintf("%02d", 1+day%28))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for index := range 3 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("rollout-%d.jsonl", index)), []byte("{}\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCodexFoldersFollowedStayBoundedAsDaysPass(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	sessions := filepath.Join(home, "sessions")
	days := 0
	for ; days < 300; days++ {
		addCodexDay(t, sessions, days)
	}
	watcher := filewake.New(filewake.DefaultLimit, t.Logf)
	t.Cleanup(func() { _ = watcher.Close() })
	wakes := newCatalogWakes([]surface.Surface{surfaces.NewCodex("")}, watcher.FollowDir)
	t.Cleanup(wakes.close)
	wakes.sync()
	settled := openDescriptors(t)
	for ; days < 360; days++ {
		previous := addCodexDay(t, sessions, days-1)
		today := addCodexDay(t, sessions, days)
		wakes.sync()
		wakes.mu.Lock()
		followed := len(wakes.watching)
		_, stale := wakes.watching[previous]
		_, current := wakes.watching[today]
		wakes.mu.Unlock()
		if followed != 4 || stale || !current {
			t.Fatalf("day %d: followed=%d previous=%v today=%v, want the sessions, year, month and today's folders", days, followed, stale, current)
		}
	}
	// Other tests in the process may still be closing descriptors of their own.
	final := openDescriptors(t)
	for deadline := time.Now().Add(5 * time.Second); final > settled && time.Now().Before(deadline); final = openDescriptors(t) {
		time.Sleep(50 * time.Millisecond)
	}
	if final > settled {
		t.Fatalf("descriptors: %d after the first sync, %d after 60 more days", settled, final)
	}
}
