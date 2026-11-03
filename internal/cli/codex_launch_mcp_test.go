package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestATerminalLaunchStartsItsThreadWithTheLaunchCredentialAndClaimsIt(t *testing.T) {
	fake := codextest.Start(t, func(method string, _ map[string]any) map[string]any {
		if method == "thread/start" {
			return map[string]any{"cwd": "/tmp/project", "thread": map[string]any{"id": "thread-launched", "source": "appServer"}}
		}
		return nil
	})
	launchID := "agenthail-launch"
	path, err := surface.ManagedCodexLaunchCredentialPath(launchID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"url":"http://127.0.0.1:7412/mcp","token":"ahs_launch","ref":"ref-launch"}`), 0600); err != nil {
		t.Fatal(err)
	}
	var claims []map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != daemon.MCPClaimPath || r.Header.Get("Authorization") != "Bearer dashboard-synthetic" {
			http.NotFound(w, r)
			return
		}
		var claim map[string]string
		_ = json.NewDecoder(r.Body).Decode(&claim)
		claims = append(claims, claim)
		w.Write([]byte(`{"claimed":true}`))
	}))
	t.Cleanup(server.Close)
	app, _ := cliFixture(t, &cliSurface{kind: surface.KindCodex})
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonAPIOverride = func() (*daemonAPIClient, error) {
		return &daemonAPIClient{baseURL: server.URL, token: "dashboard-synthetic", httpClient: server.Client()}, nil
	}
	codex, err := app.managedCodexForLaunch(launchID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("launch credential left on disk: %v", err)
	}
	session, err := codex.PrepareManagedTerminalSession(context.Background(), "/tmp/project", "")
	if err != nil || session.ID != "thread-launched" {
		t.Fatalf("session=%+v err=%v", session, err)
	}
	starts := fake.Calls("thread/start")
	config, _ := starts[0]["config"].(map[string]any)
	servers, _ := config["mcp_servers"].(map[string]any)
	server0, _ := servers["agenthail"].(map[string]any)
	headers, _ := server0["http_headers"].(map[string]any)
	if len(starts) != 1 || server0["url"] != "http://127.0.0.1:7412/mcp" || headers["Authorization"] != "Bearer ahs_launch" {
		t.Fatalf("thread/start config=%v", starts[0]["config"])
	}
	if len(claims) != 1 || claims[0]["ref"] != "ref-launch" || claims[0]["threadId"] != "thread-launched" {
		t.Fatalf("claims=%v", claims)
	}
}

func TestATerminalLaunchWithoutACredentialStartsPlain(t *testing.T) {
	codextest.Home(t)
	app, _ := cliFixture(t, &cliSurface{kind: surface.KindCodex})
	if _, err := app.managedCodexForLaunch("agenthail-plain"); err != nil {
		t.Fatal(err)
	}
}
