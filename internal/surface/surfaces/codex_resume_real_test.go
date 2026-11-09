package surfaces

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
	"github.com/zm2231/agenthail/internal/surface"
)

type slowResponses struct {
	mockResponses
	delay time.Duration
}

func (m *slowResponses) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	time.Sleep(m.delay)
	m.mockResponses.ServeHTTP(w, r)
}

type everyThreadLive struct{ fakeCredentials }

func (everyThreadLive) Live(string) (string, bool) { return "ahs_live", true }

// rolloutTurnEnd reads how a turn ended from the thread's rollout: a read
// can report a running turn as interrupted while Codex settles its state.
func rolloutTurnEnd(t *testing.T, threadID, turnID string) string {
	t.Helper()
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		paths, _ := filepath.Glob(filepath.Join(os.Getenv("CODEX_HOME"), "sessions", "*", "*", "*", "*"+threadID+".jsonl"))
		if len(paths) != 1 {
			continue
		}
		data, err := os.ReadFile(paths[0])
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			payload := gjson.Get(line, "payload")
			kind := payload.Get("type").String()
			if kind != "task_complete" && kind != "turn_aborted" {
				continue
			}
			if id := payload.Get("turn_id").String(); id == turnID || id == "" && kind == "turn_aborted" {
				return kind
			}
		}
	}
	t.Fatalf("turn %s did not end", turnID)
	return ""
}

func followingRuntime(t *testing.T) (*Codex, codexClient) {
	t.Helper()
	realManagedCodexWith(t, &slowResponses{delay: 2 * time.Second})
	codex := isolatedManagedRuntime(t)
	codex.SetCodexConfigStore(newMemoryCodexConfig())
	codex.SetMCPCredentials(&everyThreadLive{})
	follower, err := dialManagedCodex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { follower.Close() })
	return codex, follower
}

func TestTheFirstTurnOfACreatedThreadSurvivesAgenthailFollowingIt(t *testing.T) {
	codex, follower := followingRuntime(t)
	ctx := context.Background()
	session, sent, err := codex.StartSession(ctx, surface.SessionStartOptions{Message: "first", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	// Codex refuses to join a new thread until its first turn ends, so this
	// follow may fail; it must not take the turn down trying.
	codex.subscribeManagedThread(ctx, follower, session.ID)
	if end := rolloutTurnEnd(t, session.ID, sent.UUID); end != "task_complete" {
		t.Fatalf("the first turn ended with %s", end)
	}
	if !codex.subscribeManagedThread(ctx, follower, session.ID) {
		t.Fatal("Agenthail cannot follow the thread once its first turn ends")
	}
}

func TestATurnSentToAnIdleThreadSurvivesAgenthailFollowingIt(t *testing.T) {
	codex, follower := followingRuntime(t)
	ctx := context.Background()
	session, sent, err := codex.StartSession(ctx, surface.SessionStartOptions{Message: "first", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if end := rolloutTurnEnd(t, session.ID, sent.UUID); end != "task_complete" {
		t.Fatalf("the first turn ended with %s", end)
	}
	session.Status = surface.StatusIdle
	sent, err = codex.Send(ctx, session, "second")
	if err != nil || !sent.Accepted {
		t.Fatalf("send to the idle thread: %+v %v", sent, err)
	}
	if !codex.subscribeManagedThread(ctx, follower, session.ID) {
		t.Fatal("Agenthail did not follow the thread")
	}
	if end := rolloutTurnEnd(t, session.ID, sent.UUID); end != "task_complete" {
		t.Fatalf("the sent turn ended with %s", end)
	}
}

func TestATurnSentToAnIdleThreadSurvivesAReloadForItsCompactWindow(t *testing.T) {
	codex, follower := followingRuntime(t)
	ctx := context.Background()
	session, sent, err := codex.StartSession(ctx, surface.SessionStartOptions{Message: "first", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if end := rolloutTurnEnd(t, session.ID, sent.UUID); end != "task_complete" {
		t.Fatalf("the first turn ended with %s", end)
	}
	session.Status = surface.StatusIdle
	sent, err = codex.Send(ctx, session, "second")
	if err != nil || !sent.Accepted {
		t.Fatalf("send to the idle thread: %+v %v", sent, err)
	}
	if _, err := codex.codexConfigStore().SetCodexConfigOverride(session.ID, codexCompactLimit, 100000); err != nil {
		t.Fatal(err)
	}
	if err := codex.reloadThread(ctx, follower, session, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if end := rolloutTurnEnd(t, session.ID, sent.UUID); end != "task_complete" {
		t.Fatalf("the sent turn ended with %s", end)
	}
}

func TestATurnAnotherClientStartsSurvivesAgenthailFollowingItWithItsOverrides(t *testing.T) {
	realManagedCodexWith(t, &slowResponses{delay: 2 * time.Second})
	ctx := context.Background()
	codex := isolatedManagedRuntime(t)
	store := newMemoryCodexConfig()
	codex.SetCodexConfigStore(store)
	owner, err := dialManagedCodex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	started, err := owner.Request(ctx, "thread/start", map[string]any{"cwd": t.TempDir()}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	threadID := gjson.Get(codexJSON(started), "result.thread.id").String()
	realTurn(t, owner, threadID, "first")
	if _, err := store.SetCodexConfigOverride(threadID, codexCompactLimit, 100000); err != nil {
		t.Fatal(err)
	}
	turn, err := owner.Request(ctx, "turn/start", map[string]any{"threadId": threadID, "input": []any{map[string]any{"type": "text", "text": "second"}}}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	owner.Close()
	follower, err := dialManagedCodex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { follower.Close() })
	if !codex.subscribeManagedThread(ctx, follower, threadID) {
		t.Fatal("Agenthail did not follow the thread")
	}
	turnID := gjson.Get(codexJSON(turn), "result.turn.id").String()
	if end := rolloutTurnEnd(t, threadID, turnID); end != "task_complete" {
		t.Fatalf("the turn ended with %s", end)
	}
}
