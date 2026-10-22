package filewake

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}

func awaitWake(t *testing.T, wake *Wake, what string) {
	t.Helper()
	select {
	case <-wake.C():
	case <-time.After(2 * time.Second):
		t.Fatalf("no wake after %s", what)
	}
}

func drain(wake *Wake) {
	for {
		select {
		case <-wake.C():
		case <-time.After(100 * time.Millisecond):
			return
		}
	}
}

func TestAnAppendWakesEveryReaderOfTheFile(t *testing.T) {
	w := New(DefaultLimit, t.Logf)
	t.Cleanup(func() { _ = w.Close() })
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	appendLine(t, path, "first")
	one, two := w.Follow(path), w.Follow(path)
	defer one.Release()
	defer two.Release()
	if one.C() == nil || two.C() == nil {
		t.Fatal("file not watched")
	}
	appendLine(t, path, "second")
	awaitWake(t, one, "append")
	awaitWake(t, two, "append")
	if len(w.files) != 1 {
		t.Fatalf("watches=%d, want one shared watch", len(w.files))
	}
}

func TestAFileRenamedOverThePathIsWatchedAfterRefresh(t *testing.T) {
	w := New(DefaultLimit, t.Logf)
	t.Cleanup(func() { _ = w.Close() })
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	appendLine(t, path, "old")
	wake := w.Follow(path)
	defer wake.Release()
	replacement := filepath.Join(dir, "replacement.jsonl")
	appendLine(t, replacement, "new")
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	awaitWake(t, wake, "rename over the path")
	wake.Refresh()
	drain(wake)
	appendLine(t, path, "appended to the new file")
	awaitWake(t, wake, "append to the replacement")
}

func TestALateRemovalOfTheReplacedFileKeepsTheNewWatch(t *testing.T) {
	w := New(DefaultLimit, t.Logf)
	t.Cleanup(func() { _ = w.Close() })
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	appendLine(t, path, "old")
	wake := w.Follow(path)
	defer wake.Release()
	replacement := filepath.Join(dir, "replacement.jsonl")
	appendLine(t, replacement, "new")
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	awaitWake(t, wake, "rename over the path")
	drain(wake)
	wake.Refresh()
	if !w.Watched(path) {
		t.Fatal("the new file is not watched after Refresh")
	}
	// The old file's removal, as the platform can report it after the reader
	// already watches the new file.
	w.notify(path, true)
	drain(wake)
	appendLine(t, path, "appended to the new file")
	awaitWake(t, wake, "append to the replacement after the old file's removal")
}

func TestReadersPastTheLimitFallBackToTheirTimer(t *testing.T) {
	w := New(1, t.Logf)
	t.Cleanup(func() { _ = w.Close() })
	dir := t.TempDir()
	first, second := filepath.Join(dir, "a.jsonl"), filepath.Join(dir, "b.jsonl")
	appendLine(t, first, "a")
	appendLine(t, second, "b")
	watched := w.Follow(first)
	defer watched.Release()
	unwatched := w.Follow(second)
	defer unwatched.Release()
	if watched.C() == nil {
		t.Fatal("first file not watched")
	}
	if unwatched.C() != nil {
		t.Fatal("a file past the limit was watched")
	}
	unwatched.Refresh()
	shared := w.Follow(first)
	if shared.C() == nil {
		t.Fatal("a second reader of a watched file was refused")
	}
	shared.Release()
	watched.Release()
	if len(w.files) != 0 {
		t.Fatalf("watch kept after its last reader: %d", len(w.files))
	}
	again := w.Follow(second)
	defer again.Release()
	if again.C() == nil {
		t.Fatal("released capacity was not reused")
	}
}

func TestAMissingFileIsWatchedOnceItAppears(t *testing.T) {
	w := New(DefaultLimit, t.Logf)
	t.Cleanup(func() { _ = w.Close() })
	path := filepath.Join(t.TempDir(), "later.jsonl")
	wake := w.Follow(path)
	defer wake.Release()
	appendLine(t, path, "created")
	wake.Refresh()
	drain(wake)
	appendLine(t, path, "appended")
	awaitWake(t, wake, "append after creation")
}

func TestARefusedWatchLeavesTheReaderOnItsTimerAndFreesTheSlot(t *testing.T) {
	w := New(1, t.Logf)
	t.Cleanup(func() { _ = w.Close() })
	dir := t.TempDir()
	refused, healthy := filepath.Join(dir, "refused.jsonl"), filepath.Join(dir, "healthy.jsonl")
	appendLine(t, refused, "a")
	appendLine(t, healthy, "b")
	if !func() bool { w.mu.Lock(); defer w.mu.Unlock(); return w.startLocked() }() {
		t.Skip("file watching is unavailable on this platform")
	}
	add := w.add
	w.add = func(path string) error {
		if path == refused {
			return errors.New("no space left on device")
		}
		return add(path)
	}
	wake := w.Follow(refused)
	defer wake.Release()
	if wake.C() != nil {
		t.Fatal("a refused watch handed the reader a channel")
	}
	if len(w.files) != 0 {
		t.Fatalf("a refused watch kept a slot: %d", len(w.files))
	}
	other := w.Follow(healthy)
	defer other.Release()
	if other.C() == nil || !w.Watched(healthy) {
		t.Fatal("the free slot was not given to a healthy file")
	}
}

func TestARefusedPathIsLoggedOnceAcrossReaders(t *testing.T) {
	var logged atomic.Int32
	w := New(4, func(string, ...any) { logged.Add(1) })
	t.Cleanup(func() { _ = w.Close() })
	path := filepath.Join(t.TempDir(), "refused.jsonl")
	appendLine(t, path, "a")
	if !func() bool { w.mu.Lock(); defer w.mu.Unlock(); return w.startLocked() }() {
		t.Skip("file watching is unavailable on this platform")
	}
	w.add = func(string) error { return errors.New("no space left on device") }
	for range 2 {
		wake := w.Follow(path)
		if wake.C() != nil {
			t.Fatal("a refused watch handed the reader a channel")
		}
		wake.Release()
	}
	if len(w.files) != 0 {
		t.Fatalf("refused watches kept slots: %d", len(w.files))
	}
	if got := logged.Load(); got != 1 {
		t.Fatalf("the refusal was logged %d times, want once", got)
	}
}

func TestRefusalMemoryStaysWithinTheWatcherLimit(t *testing.T) {
	w := New(2, func(string, ...any) {})
	t.Cleanup(func() { _ = w.Close() })
	dir := t.TempDir()
	if !func() bool { w.mu.Lock(); defer w.mu.Unlock(); return w.startLocked() }() {
		t.Skip("file watching is unavailable on this platform")
	}
	w.add = func(string) error { return errors.New("no space left on device") }
	for index := range 10 {
		path := filepath.Join(dir, fmt.Sprintf("refused-%d.jsonl", index))
		appendLine(t, path, "a")
		wake := w.Follow(path)
		if wake.C() != nil {
			t.Fatal("a refused watch handed the reader a channel")
		}
		wake.Release()
	}
	if len(w.files) != 0 || len(w.refused) > 2 {
		t.Fatalf("files=%d refused=%d", len(w.files), len(w.refused))
	}
}
