package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestMoveToAnotherRootReachesTheDaemon(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	session := surface.Session{ID: "limited", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "limited work"}
	app, reg := cliFixture(t, &cliSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{session.ID: session}})
	if err := reg.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var moves []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/session-moves" {
			_ = json.NewEncoder(w).Encode(map[string]any{"matches": []remoteTarget{}})
			return
		}
		body, _ := io.ReadAll(r.Body)
		var request map[string]any
		_ = json.Unmarshal(body, &request)
		mu.Lock()
		moves = append(moves, request)
		mu.Unlock()
		_, _ = io.WriteString(w, `{"ok":true,"result":{"root":"pool"}}`)
	}))
	t.Cleanup(server.Close)
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonAPIOverride = func() (*daemonAPIClient, error) {
		return &daemonAPIClient{baseURL: server.URL, token: "dashboard-synthetic", httpClient: server.Client()}, nil
	}
	output, err := captureStdout(t, func() error { return app.Run([]string{"move", "limited", "--root", "pool"}) })
	if err != nil || !strings.Contains(output, "moved limited work (limited) to account pool") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(moves) != 1 || moves[0]["sessionId"] != "limited" || moves[0]["root"] != "pool" || moves[0]["host"] != "" {
		t.Fatalf("moves=%v", moves)
	}
}
