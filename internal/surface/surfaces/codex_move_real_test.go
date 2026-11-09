package surfaces

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/zm2231/agenthail/internal/surface"
)

type codexMachine struct {
	home, codexHome, cwd string
	codex                *Codex
}

func realCodexMachine(t *testing.T) *codexMachine {
	t.Helper()
	realManagedCodex(t)
	machine := &codexMachine{home: os.Getenv("HOME"), codexHome: os.Getenv("CODEX_HOME"), cwd: t.TempDir(), codex: isolatedManagedRuntime(t)}
	machine.codex.SetCodexConfigStore(newMemoryCodexConfig())
	return machine
}

// use makes this machine's homes the ones the adapter reads, as if the test
// ran on it.
func (m *codexMachine) use(t *testing.T) codexClient {
	t.Helper()
	t.Setenv("HOME", m.home)
	t.Setenv("CODEX_HOME", m.codexHome)
	client, err := dialManagedCodex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func (m *codexMachine) moveOut(t *testing.T, threadID string) (*surface.MoveOut, string, *bytes.Buffer) {
	t.Helper()
	m.use(t)
	ctx := context.Background()
	session, err := m.codex.Resolve(ctx, threadID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := m.codex.PrepareMove(ctx, session, surface.AdoptRequest{})
	if err != nil {
		t.Fatal(err)
	}
	defer out.Release()
	moveID := uuid.NewString()
	if err := m.codex.ArchiveMove(threadID, moveID); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := m.codex.WriteMoveArchive(&archive, threadID, moveID); err != nil {
		t.Fatal(err)
	}
	return out, moveID, &archive
}

func (m *codexMachine) moveIn(t *testing.T, out *surface.MoveOut, moveID string, archive *bytes.Buffer) *surface.Session {
	t.Helper()
	m.use(t)
	in := surface.MoveIn{MoveID: moveID, Conversation: out.Conversation, Cwd: m.cwd, Title: out.Title, Size: out.Size, SHA256: out.SHA256, PartsSize: out.PartsSize, State: out.State}
	if err := m.codex.CheckMoveIn(in); err != nil {
		t.Fatal(err)
	}
	session, err := m.codex.ReceiveMove(context.Background(), in, archive, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func threadTurns(t *testing.T, client codexClient, threadID string) int {
	t.Helper()
	response, err := client.Request(context.Background(), "thread/turns/list", map[string]any{"threadId": threadID, "limit": 50}, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return len(gjson.Get(codexJSON(response), "result.data").Array())
}

func TestARealCodexThreadMovesToAnotherCodexHomeAndBack(t *testing.T) {
	source, target := realCodexMachine(t), realCodexMachine(t)
	ctx := context.Background()

	owner := source.use(t)
	started, err := owner.Request(ctx, "thread/start", map[string]any{"cwd": source.cwd}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	threadID := gjson.Get(codexJSON(started), "result.thread.id").String()
	realTurn(t, owner, threadID, "first")
	for method, params := range map[string]map[string]any{
		"thread/name/set": {"threadId": threadID, "name": "moving thread"},
		"thread/goal/set": {"threadId": threadID, "objective": "carry the goal", "status": "paused"},
	} {
		if _, err := owner.Request(ctx, method, params, 10*time.Second); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}

	session, err := source.codex.Resolve(ctx, threadID)
	if err != nil {
		t.Fatal(err)
	}
	held, err := source.codex.PrepareMove(ctx, session, surface.AdoptRequest{})
	if err != nil {
		t.Fatal(err)
	}
	session.Status = surface.StatusIdle
	if _, err := source.codex.Send(ctx, session, "during the move"); !errors.Is(err, errCodexThreadMoving) {
		t.Fatalf("a send while the move holds the thread returned %v", err)
	}
	restoredMove := uuid.NewString()
	if err := source.codex.ArchiveMove(threadID, restoredMove); err != nil {
		t.Fatal(err)
	}
	held.Release()
	if _, err := owner.Request(ctx, "thread/resume", map[string]any{"threadId": threadID}, 30*time.Second); err == nil {
		t.Fatal("the source resumed a thread its move archived")
	}
	if err := source.codex.RestoreMove(threadID, restoredMove); err != nil {
		t.Fatal(err)
	}
	mustRequest(t, owner, "thread/resume", map[string]any{"threadId": threadID})
	realTurn(t, owner, threadID, "after the restored move")

	out, moveID, archive := source.moveOut(t, threadID)
	archived, err := codexThreadRollouts(filepath.Join(source.codexHome, "archived_sessions"), threadID, false)
	if err != nil || len(archived) != 1 {
		t.Fatalf("archived rollouts=%v err=%v", archived, err)
	}
	if size, digest, err := hashFile(archived[0]); err != nil || size != out.Size || digest != out.SHA256 {
		t.Fatalf("archiving changed the rollout the move declared: %d bytes, digest %s (%v)", size, digest, err)
	}

	moved := target.moveIn(t, out, moveID, archive)
	client := target.use(t)
	if moved.ID != threadID || moved.Cwd != target.cwd {
		t.Fatalf("the target runs %s in %s, want %s in %s", moved.ID, moved.Cwd, threadID, target.cwd)
	}
	thread := gjson.Get(codexJSON(mustRequest(t, client, "thread/read", map[string]any{"threadId": threadID})), "result.thread")
	goal := gjson.Get(codexJSON(mustRequest(t, client, "thread/goal/get", map[string]any{"threadId": threadID})), "result.goal")
	if thread.Get("name").String() != "moving thread" || goal.Get("objective").String() != "carry the goal" || goal.Get("status").String() != "paused" {
		t.Fatalf("the target lost the thread's state: name=%s goal=%s", thread.Get("name"), goal)
	}
	before := threadTurns(t, client, threadID)
	if before < 2 {
		t.Fatalf("the target lists %d turns of the moved thread", before)
	}
	realTurn(t, client, threadID, "on the target")

	back, backID, backArchive := target.moveOut(t, threadID)
	returned := source.moveIn(t, back, backID, backArchive)
	if returned.Cwd != source.cwd {
		t.Fatalf("the source runs the returned thread in %s, want %s", returned.Cwd, source.cwd)
	}
	client = source.use(t)
	if turns := threadTurns(t, client, threadID); turns != before+1 {
		t.Fatalf("the returned thread lists %d turns, want %d", turns, before+1)
	}
	realTurn(t, client, threadID, "back on the source")
}

func mustRequest(t *testing.T, client codexClient, method string, params map[string]any) map[string]any {
	t.Helper()
	response, err := client.Request(context.Background(), method, params, 10*time.Second)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	return response
}
