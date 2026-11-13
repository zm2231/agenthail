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

func TestANewEntryWakesTheDirectoryButAWriteToAnExistingOneDoesNot(t *testing.T) {
	w := New(DefaultLimit, t.Logf)
	t.Cleanup(func() { _ = w.Close() })
	dir := t.TempDir()
	existing := filepath.Join(dir, "100.json")
	appendLine(t, existing, "{}")
	other := t.TempDir()
	wake := w.FollowDir(dir)
	defer wake.Release()
	if wake.C() == nil {
		t.Fatal("directory not watched")
	}
	appendLine(t, existing, "{}")
	appendLine(t, filepath.Join(other, "elsewhere.json"), "{}")
	select {
	case <-wake.C():
		t.Fatal("a write to an existing entry, or a file in another directory, woke the directory")
	case <-time.After(300 * time.Millisecond):
	}
	appendLine(t, filepath.Join(dir, "200.json"), "{}")
	awaitWake(t, wake, "a new entry")
	drain(wake)
	staged := filepath.Join(other, "staged.json")
	appendLine(t, staged, "{}")
	if err := os.Rename(staged, filepath.Join(dir, "300.json")); err != nil {
		t.Fatal(err)
	}
	awaitWake(t, wake, "an entry moved in")
}

func TestAFileAndItsDirectoryAreFollowedApart(t *testing.T) {
	w := New(DefaultLimit, t.Logf)
	t.Cleanup(func() { _ = w.Close() })
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	appendLine(t, path, "first")
	file, folder := w.Follow(path), w.FollowDir(dir)
	defer file.Release()
	folder.Release()
	appendLine(t, path, "second")
	awaitWake(t, file, "an append after the directory reader left")
	if !w.Watched(path) {
		t.Fatal("releasing the directory dropped the file's watch")
	}
}

func TestAFolderCreatedInAFollowedFolderWakesForItsOwnNewEntries(t *testing.T) {
	w := New(DefaultLimit, t.Logf)
	t.Cleanup(func() { _ = w.Close() })
	for round := range 50 {
		root := t.TempDir()
		parent := w.FollowDir(root)
		day := filepath.Join(root, "06")
		if err := os.Mkdir(day, 0700); err != nil {
			t.Fatal(err)
		}
		awaitWake(t, parent, "a new folder")
		child := w.FollowDir(day)
		if child.C() == nil {
			t.Fatal("new folder not watched")
		}
		if w.dirs[root].own == w.dirs[day].own {
			t.Fatal("a folder and its parent share a platform watcher")
		}
		appendLine(t, filepath.Join(root, "sibling.json"), "{}")
		awaitWake(t, parent, "a sibling of the new folder")
		appendLine(t, filepath.Join(day, fmt.Sprintf("%d.json", round)), "{}")
		awaitWake(t, child, "the new folder's first entry")
		parent.Release()
		appendLine(t, filepath.Join(day, "after-parent.json"), "{}")
		awaitWake(t, child, "an entry after the parent's reader left")
		child.Release()
	}
}

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

func armed(w *Watcher, dir string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	folder := w.dirs[dir]
	return folder != nil && folder.armed
}

func TestFollowingAndReleasingAFolderLeavesNoDescriptorOpen(t *testing.T) {
	w := New(DefaultLimit, t.Logf)
	t.Cleanup(func() { _ = w.Close() })
	dir := t.TempDir()
	for index := range 20 {
		appendLine(t, filepath.Join(dir, fmt.Sprintf("%d.json", index)), "{}")
	}
	before := openDescriptors(t)
	for round := range 100 {
		wake := w.FollowDir(dir)
		if wake.C() == nil {
			t.Fatal("directory not watched")
		}
		appendLine(t, filepath.Join(dir, fmt.Sprintf("new-%d.json", round)), "{}")
		appendLine(t, filepath.Join(dir, "0.json"), "{}")
		wake.Release()
	}
	if after := openDescriptors(t); after != before {
		t.Fatalf("descriptors: %d before, %d after following and releasing the folder 100 times", before, after)
	}
}

func TestAFollowedFolderRemovedOrRenamedDropsItsWatch(t *testing.T) {
	w := New(DefaultLimit, t.Logf)
	t.Cleanup(func() { _ = w.Close() })
	root := t.TempDir()
	removed, renamed := filepath.Join(root, "removed"), filepath.Join(root, "renamed")
	for _, dir := range []string{removed, renamed} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		wake := w.FollowDir(dir)
		defer wake.Release()
		if !armed(w, dir) {
			t.Fatalf("%s not watched", dir)
		}
	}
	if err := os.Remove(removed); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(renamed, filepath.Join(root, "moved")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for armed(w, removed) || armed(w, renamed) {
		if time.Now().After(deadline) {
			t.Fatalf("removed armed=%v renamed armed=%v, want both watches dropped", armed(w, removed), armed(w, renamed))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
