package ledger

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
)

const lockProject = "repo:github.com/acme/widget"

func openRegistry(t *testing.T, path string) *registry.Registry {
	t.Helper()
	r, err := registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func TestACaptureWaitsForTheBoardAndLeavesItCaughtUp(t *testing.T) {
	db := openRegistry(t, registrytest.Path(t)).WriteDB()
	s := NewCachedStore(db, db)
	board, release, err := s.lockBoard(lockProject)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := s.RecordUtterance(Utterance{ProjectKey: lockProject, Ref: "claude-prompt:1", SpeakerID: "person", TargetID: "worker", Text: "stop, I'm here", CapturedBy: CapturedByHook})
		done <- err
	}()
	select {
	case err := <-done:
		release()
		t.Fatalf("a capture committed while a gate held the board (err %v)", err)
	case <-time.After(150 * time.Millisecond):
	}
	if n := len(board.Utterances()); n != 0 {
		t.Fatalf("board saw %d utterances while locked", n)
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := len(board.Utterances()); n != 1 {
		t.Fatalf("the cached board holds %d utterances after the capture, want 1", n)
	}
}

func TestAFailedCatchUpDropsTheCachedBoard(t *testing.T) {
	path := registrytest.Path(t)
	writer := openRegistry(t, path)
	reader, err := registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s := NewCachedStore(writer.WriteDB(), reader.ReadDB())
	if _, err := s.Board(lockProject); err != nil {
		t.Fatal(err)
	}
	if s.cache.boards[lockProject] == nil {
		t.Fatal("the board was not cached")
	}
	reader.Close()
	if _, err := s.Board(lockProject); err == nil {
		t.Fatal("catch-up on a closed read connection succeeded")
	}
	if s.cache.boards[lockProject] != nil {
		t.Fatal("a board that failed to catch up stayed cached")
	}
}
