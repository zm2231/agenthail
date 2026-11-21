package surfaces

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func finishedCodexThread(t *testing.T) (*codextest.Daemon, *Codex, surface.Session) {
	t.Helper()
	daemon := startManagedCodex(t, nil)
	id := "019f004a-0000-7000-8000-0000000000d1"
	daemon.AddThread(codextest.Thread{ID: id, Cwd: t.TempDir(), Turns: []codextest.Turn{{ID: "turn-1", Status: "inProgress"}}})
	daemon.Load(id)
	daemon.CompleteTurn(id, "done")
	codex := isolatedManagedRuntime(t)
	sessions, err := codex.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		if session.ID == id {
			return daemon, codex, session
		}
	}
	t.Fatalf("thread %s not listed", id)
	return nil, nil, surface.Session{}
}

func TestCodexListsAFinishedTurnAsUnread(t *testing.T) {
	_, _, session := finishedCodexThread(t)
	if session.ReadState == nil || session.ReadState.FirstUnread != "turn-1" || session.ReadState.Revision == "" {
		t.Fatalf("read state = %+v, want unread at turn-1", session.ReadState)
	}
}

func TestCodexMarksReadAtTheRevisionSeen(t *testing.T) {
	daemon, codex, session := finishedCodexThread(t)
	state, err := codex.SetReadState(context.Background(), &session, false, session.ReadState.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if state.Unread() || state.Revision == session.ReadState.Revision {
		t.Fatalf("marked state = %+v", state)
	}
	if current := daemon.ReadState(session.ID); current["firstUnread"] != nil {
		t.Fatalf("Codex receipt = %v, want read", current)
	}
}

func TestCodexRefusesAMarkAgainstAStaleRevision(t *testing.T) {
	daemon, codex, session := finishedCodexThread(t)
	_, err := codex.SetReadState(context.Background(), &session, false, "rev-stale")
	var conflict *surface.ReadStateConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v, want a read state conflict", err)
	}
	if conflict.Current != *session.ReadState {
		t.Fatalf("conflict carried %+v, want %+v", conflict.Current, *session.ReadState)
	}
	if calls := len(daemon.Calls("thread/readState/update")); calls != 1 {
		t.Fatalf("update calls = %d, want 1 with no retry", calls)
	}
	if current := daemon.ReadState(session.ID); current["firstUnread"] == nil {
		t.Fatalf("a refused mark changed the receipt: %v", current)
	}
}

func TestCodexWatchWakesTheCatalogWhenAReceiptChanges(t *testing.T) {
	daemon := startManagedCodex(t, nil)
	id := "019f004a-0000-7000-8000-0000000000d2"
	daemon.AddThread(codextest.Thread{ID: id, Cwd: t.TempDir(), Turns: []codextest.Turn{{ID: "turn-1", Status: "inProgress"}}})
	daemon.Load(id)
	var wakes atomic.Int32
	watchedCodexWaking(t, id, func() { wakes.Add(1) })
	testkit.Eventually(t, "the watch to attach", 5*time.Second, func() bool { return wakes.Load() > 0 })
	attached := wakes.Load()
	daemon.CompleteTurn(id, "done")
	testkit.Eventually(t, "the finished turn's receipt to wake the catalog", 5*time.Second, func() bool { return wakes.Load() > attached })
}
