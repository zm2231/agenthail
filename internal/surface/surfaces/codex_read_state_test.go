package surfaces

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	marked, err := codex.OpenRead(context.Background(), &session, session.ReadState.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	state := *marked
	if state.Unread() || state.Revision == session.ReadState.Revision {
		t.Fatalf("marked state = %+v", state)
	}
	if current := daemon.ReadState(session.ID); current["firstUnread"] != nil {
		t.Fatalf("Codex receipt = %v, want read", current)
	}
}

func TestCodexRefusesAMarkAgainstAStaleRevision(t *testing.T) {
	daemon, codex, session := finishedCodexThread(t)
	_, err := codex.OpenRead(context.Background(), &session, "rev-stale", true)
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

func unreadUnloadedThread(t *testing.T, originator string) (*codextest.Daemon, *Codex, surface.Session) {
	t.Helper()
	daemon := startManagedCodex(t, nil)
	id := "019f004a-0000-7000-8000-0000000000d3"
	daemon.AddThread(codextest.Thread{ID: id, Cwd: t.TempDir(), Originator: originator, Turns: []codextest.Turn{{ID: "turn-1", Status: "inProgress"}}})
	daemon.CompleteTurn(id, "done")
	receipt := codexReadState(daemon.ReadState(id))
	return daemon, isolatedManagedRuntime(t), surface.Session{ID: id, Surface: surface.KindCodex, ReadState: receipt}
}

func TestCodexReadingAConversationLoadsItIntoItsWriter(t *testing.T) {
	daemon, codex, session := unreadUnloadedThread(t, "agenthail")
	if _, err := codex.OpenRead(context.Background(), &session, session.ReadState.Revision, true); err != nil {
		t.Fatal(err)
	}
	if !daemon.Loaded(session.ID) {
		t.Fatal("reading the conversation left it unloaded, so a send would still have to load it")
	}
	if receipt := daemon.ReadState(session.ID); receipt["firstUnread"] != nil {
		t.Fatalf("Codex receipt = %v, want read", receipt)
	}
}

func TestCodexReadingADesktopConversationWhileDesktopIsDownOnlyMarksIt(t *testing.T) {
	daemon, codex, session := unreadUnloadedThread(t, codexDesktopOriginator)
	if _, err := codex.OpenRead(context.Background(), &session, session.ReadState.Revision, true); err != nil {
		t.Fatal(err)
	}
	if daemon.Loaded(session.ID) || len(daemon.Calls("thread/resume")) != 0 {
		t.Fatal("reading took Desktop's conversation into the daemon")
	}
	if receipt := daemon.ReadState(session.ID); receipt["firstUnread"] != nil {
		t.Fatalf("Codex receipt = %v, want read", receipt)
	}
}

func TestCodexMarkingUnreadLoadsNothing(t *testing.T) {
	daemon, codex, session := unreadUnloadedThread(t, "agenthail")
	if _, err := codex.MarkUnread(context.Background(), &session, session.ReadState.Revision); err != nil {
		t.Fatal(err)
	}
	if daemon.Loaded(session.ID) || len(daemon.Calls("thread/resume")) != 0 {
		t.Fatal("marking unread loaded the conversation")
	}
}

func TestCodexOpeningALiveConversationCostsFourRequestsAndNoResume(t *testing.T) {
	daemon, codex, session := finishedCodexThread(t)
	before := len(daemon.Methods())
	if _, err := codex.OpenRead(context.Background(), &session, session.ReadState.Revision, true); err != nil {
		t.Fatal(err)
	}
	if sent := fmt.Sprint(daemon.Methods()[before:]); sent != "[initialize thread/loaded/list thread/read thread/read thread/readState/update]" {
		t.Fatalf("opening a live conversation and marking it read sent %s", sent)
	}
	before = len(daemon.Methods())
	if state, err := codex.OpenRead(context.Background(), &session, session.ReadState.Revision, false); err != nil || state != nil {
		t.Fatalf("opening a read conversation returned %+v, %v", state, err)
	}
	if sent := fmt.Sprint(daemon.Methods()[before:]); sent != "[initialize thread/loaded/list thread/read thread/read]" {
		t.Fatalf("opening a read conversation sent %s, want no mark", sent)
	}
}

func TestCodexOpeningAConversationNeverStartsTheDaemon(t *testing.T) {
	lifecycle := codextest.InstallLifecycle(t, codextest.Home(t), nil)
	rollout := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(rollout, []byte(`{"type":"session_meta","payload":{"originator":"agenthail"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session := surface.Session{ID: managedThreadID, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transcript: rollout}
	codex := isolatedManagedRuntime(t)
	_, _ = codex.OpenRead(context.Background(), &session, "rev-1", true)
	_, _ = codex.OpenRead(context.Background(), &session, "rev-1", false)
	_, _ = codex.MarkUnread(context.Background(), &session, "rev-1")
	if commands := lifecycle.Commands(); len(commands) != 0 {
		t.Fatalf("opening a conversation ran %v", commands)
	}
}
