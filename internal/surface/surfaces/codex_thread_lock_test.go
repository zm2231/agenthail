package surfaces

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
)

func movableCodexThread(t *testing.T) (*Codex, *surface.Session) {
	t.Helper()
	fake := startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
		switch method {
		case "thread/list", "thread/queue/list":
			return map[string]any{"data": []any{}}
		case "thread/goal/get":
			return map[string]any{}
		}
		return nil
	})
	fake.AddThread(codextest.Thread{ID: managedThreadID, Cwd: t.TempDir(), Originator: "agenthail", Source: "agenthail"})
	if err := os.MkdirAll(filepath.Dir(codexThreadLockPath(managedThreadID)), 0700); err != nil {
		t.Fatal(err)
	}
	session := &surface.Session{ID: managedThreadID, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Source: "agenthail", Transport: codexTransportManaged, Status: surface.StatusIdle}
	return isolatedManagedRuntime(t), session
}

func codexThreadLocks(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(codexThreadLockPath(managedThreadID)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestSendsToACodexThreadLeaveNoLockFileBehind(t *testing.T) {
	codex, session := movableCodexThread(t)
	if _, err := codex.Send(context.Background(), session, "hello"); err != nil {
		t.Fatal(err)
	}
	if locks := codexThreadLocks(t); len(locks) != 0 {
		t.Fatalf("a send left thread lock files %v", locks)
	}
}

func TestAMoveRefusesSendsUntilItEndsAndLeavesNoLockFile(t *testing.T) {
	codex, session := movableCodexThread(t)
	out, err := codex.PrepareMove(context.Background(), session, surface.AdoptRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codex.Send(context.Background(), session, "during the move"); !errors.Is(err, errCodexThreadMoving) {
		t.Fatalf("a send during the move returned %v", err)
	}
	waiting, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if second, err := codex.PrepareMove(waiting, session, surface.AdoptRequest{}); err == nil {
		second.Release()
		t.Fatal("a second move held the thread while the first still held it")
	}
	out.Release()
	if locks := codexThreadLocks(t); len(locks) != 0 {
		t.Fatalf("an ended move left thread lock files %v", locks)
	}
	again, err := codex.PrepareMove(context.Background(), session, surface.AdoptRequest{})
	if err != nil {
		t.Fatalf("a later move could not hold the thread: %v", err)
	}
	again.Release()
	if _, err := codex.Send(context.Background(), session, "after the moves"); err != nil {
		t.Fatalf("a send after the moves ended returned %v", err)
	}
}

func TestALockFileLeftByAnUnfinishedMoveIsRemovedByTheNextSend(t *testing.T) {
	codex, session := movableCodexThread(t)
	if err := os.WriteFile(codexThreadLockPath(managedThreadID), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := codex.Send(context.Background(), session, "after an unfinished move"); err != nil {
		t.Fatalf("a send after an unfinished move returned %v", err)
	}
	if locks := codexThreadLocks(t); len(locks) != 0 {
		t.Fatalf("the unfinished move's lock file is still there: %v", locks)
	}
}
