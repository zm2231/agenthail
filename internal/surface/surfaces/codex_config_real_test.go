package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tidwall/gjson"
	"github.com/zm2231/agenthail/internal/surface"
)

type mockResponses struct {
	mu       sync.Mutex
	requests int
}

func (m *mockResponses) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	m.mu.Lock()
	m.requests++
	id := fmt.Sprintf("resp_%d", m.requests)
	m.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(event map[string]any) {
		data, _ := json.Marshal(event)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data)
	}
	send(map[string]any{"type": "response.created", "response": map[string]any{"id": id}})
	send(map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "message", "role": "assistant", "id": "msg_" + id, "content": []any{map[string]any{"type": "output_text", "text": "ok"}}}})
	send(map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "usage": map[string]any{"input_tokens": 9000, "output_tokens": 10, "total_tokens": 9010, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens_details": map[string]any{"reasoning_tokens": 0}}}})
}

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

const realReloadCodexVersion = "0.151.0"

// realManagedCodex runs a real `codex app-server` on the managed control
// socket of a scratch CODEX_HOME, with a mock model that always reports
// 9000 input tokens.
func realManagedCodex(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex is not installed")
	}
	out, err := exec.Command("codex", "--version").Output()
	if err != nil || !codexAtLeast(strings.TrimPrefix(strings.TrimSpace(string(out)), "codex-cli "), realReloadCodexVersion) {
		t.Skipf("codex %q is older than %s, the oldest whose reload this test was checked against", out, realReloadCodexVersion)
	}
	server := httptest.NewServer(&mockResponses{})
	t.Cleanup(server.Close)
	home, err := os.MkdirTemp("/tmp", "ah-cx-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	codexHome := filepath.Join(home, "codex")
	for _, dir := range []string{codexHome, filepath.Join(home, "tmp"), filepath.Join(codexHome, "app-server-control")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	config := fmt.Sprintf("model = \"probe-model\"\nmodel_provider = \"mock\"\n[model_providers.mock]\nname = \"mock\"\nbase_url = \"%s/v1\"\nwire_api = \"responses\"\nenv_key = \"MOCK_KEY\"\n", server.URL)
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("HOME", home)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cmd := processGroupCommand(ctx, "codex", "app-server", "--disable", "plugins", "--listen", "unix://"+managedCodexSocketPath())
	cmd.Env = []string{"HOME=" + home, "CODEX_HOME=" + codexHome, "PATH=" + os.Getenv("PATH"), "MOCK_KEY=x", "TMPDIR=" + filepath.Join(home, "tmp")}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.Now().Add(20 * time.Second)
	for {
		if client, err := dialManagedCodex(context.Background()); err == nil {
			_ = client.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the real app-server did not listen on the control socket")
		}
		time.Sleep(100 * time.Millisecond)
	}
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
