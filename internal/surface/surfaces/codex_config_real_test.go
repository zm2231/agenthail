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
	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
)

type mockResponses = codextest.MockModel

func rolloutCompactions(t *testing.T, threadID string) int {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(os.Getenv("CODEX_HOME"), "sessions", "*", "*", "*", "*"+threadID+".jsonl"))
	if len(paths) != 1 {
		t.Fatalf("rollouts for %s: %v", threadID, paths)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), `"type":"compacted"`)
}

func realManagedCodex(t *testing.T) {
	t.Helper()
	realManagedCodexWith(t, &mockResponses{})
}

// realManagedCodexWith points CODEX_HOME and HOME at a real app-server's
// homes and returns its restart.
func realManagedCodexWith(t *testing.T, model http.Handler) (restart func()) {
	t.Helper()
	real := codextest.StartReal(t, model)
	t.Setenv("CODEX_HOME", real.CodexHome)
	t.Setenv("HOME", real.Home)
	return real.Restart
}

func realTurn(t *testing.T, client codexClient, threadID, text string) {
	t.Helper()
	ctx := context.Background()
	count := func() (int, string) {
		response, err := client.Request(ctx, "thread/read", map[string]any{"threadId": threadID, "includeTurns": true}, 10*time.Second)
		if err != nil {
			return 0, ""
		}
		turns := gjson.Get(codexJSON(response), "result.thread.turns").Array()
		if len(turns) == 0 {
			return 0, ""
		}
		return len(turns), turns[len(turns)-1].Get("status").String()
	}
	before, _ := count()
	if _, err := client.Request(ctx, "turn/start", map[string]any{"threadId": threadID, "input": []any{map[string]any{"type": "text", "text": text}}}, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if turns, status := count(); turns > before && status == "completed" {
			return
		}
	}
	t.Fatalf("turn %q did not complete", text)
}

func TestARealCodexThreadTakesItsCompactWindowWhileAgenthailFollowsIt(t *testing.T) {
	realManagedCodex(t)
	ctx := context.Background()
	codex := isolatedManagedRuntime(t)
	codex.SetCodexConfigStore(newMemoryCodexConfig())
	owner, err := dialManagedCodex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Close() })
	started, err := owner.Request(ctx, "thread/start", map[string]any{"cwd": t.TempDir()}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	threadID := gjson.Get(codexJSON(started), "result.thread.id").String()
	session := &surface.Session{ID: threadID, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Source: "agenthail", Transport: codexTransportManaged, Status: surface.StatusIdle}
	realTurn(t, owner, threadID, "first")
	if _, err := owner.Request(ctx, "thread/unsubscribe", map[string]any{"threadId": threadID}, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	follower, err := dialManagedCodex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { follower.Close() })
	if !codex.subscribeManagedThread(ctx, follower, threadID) {
		t.Fatal("the follower did not subscribe")
	}

	if err := codex.SetCompactSetting(ctx, session, surface.CompactSettingWindow, "100000"); err != nil {
		t.Fatal(err)
	}
	realTurn(t, follower, threadID, "second")
	realTurn(t, follower, threadID, "third")
	if got := rolloutCompactions(t, threadID); got != 0 {
		t.Fatalf("a limit of 100000 compacted a 9000-token context %d times", got)
	}
	if _, err := codex.codexThreadRequest(ctx, follower, codexThreadResume, session, map[string]any{"threadId": threadID}, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	store := codex.codexConfigStore()
	if _, err := store.SetCodexConfigOverride(threadID, codexCompactLimit, 5000); err != nil {
		t.Fatal(err)
	}
	if err := codex.reloadThread(ctx, follower, session, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	realTurn(t, follower, threadID, "fourth")
	realTurn(t, follower, threadID, "fifth")
	if rolloutCompactions(t, threadID) == 0 {
		t.Fatal("Codex did not take the thread's limit of 5000 while Agenthail followed the thread")
	}
}
