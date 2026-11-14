package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

type unloadedCodex struct {
	app  *App
	r    *registry.Registry
	fake *codextest.Daemon
	id   string
}

// unloadedCodexThread is a Codex thread the app-server has on disk but not
// loaded, registered in a CLI that runs its own Codex adapter.
func unloadedCodexThread(t *testing.T) unloadedCodex {
	t.Helper()
	for _, key := range []string{"AGENTHAIL_SESSION_ID", "CODEX_THREAD_ID", "CLAUDE_CODE_SESSION_ID"} {
		t.Setenv(key, "")
	}
	fake := codextest.Start(t, func(method string, _ map[string]any) map[string]any {
		if method == "thread/fork" {
			return map[string]any{"thread": map[string]any{"id": uuid.NewString(), "source": "agenthail"}}
		}
		return nil
	})
	id := uuid.NewString()
	cwd := t.TempDir()
	fake.AddThread(codextest.Thread{ID: id, Originator: "agenthail", Source: "agenthail", Cwd: cwd, Turns: []codextest.Turn{{ID: "turn-1", Status: "completed", User: "start", Assistant: "started"}}})
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	if err := r.RegisterSession(surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transport: "managed", Status: surface.StatusIdle, Cwd: cwd}); err != nil {
		t.Fatal(err)
	}
	app := &App{Registry: r, Surfaces: []SurfaceEntry{{Name: "codex", Surface: surfaces.NewCodex("1")}}, DefaultTimeout: 10 * time.Second, catalogDaemonRunning: func() bool { return false }}
	return unloadedCodex{app: app, r: r, fake: fake, id: id}
}

func (c unloadedCodex) serveDaemon(t *testing.T) {
	t.Helper()
	serveAppDaemon(t, c.app, c.r, []surface.Surface{surfaces.NewCodex("1")})
}

func agenthailBearer(config map[string]any) string {
	servers, _ := config["mcp_servers"].(map[string]any)
	server, _ := servers["agenthail"].(map[string]any)
	headers, _ := server["http_headers"].(map[string]any)
	bearer, _ := headers["Authorization"].(string)
	return strings.TrimPrefix(bearer, "Bearer ")
}

func codexCommands(id string) map[string][]string {
	return map[string][]string{
		"send": {"send", id, "hello"},
		"fork": {"thread", "fork", id},
	}
}

func TestACommandThatMustLoadACodexThreadWithoutADaemonSaysToStartIt(t *testing.T) {
	for _, name := range []string{"send", "fork"} {
		t.Run(name, func(t *testing.T) {
			c := unloadedCodexThread(t)
			args := codexCommands(c.id)[name]
			output, err := captureStdout(t, func() error { return c.app.Run(args) })
			if err == nil || !strings.Contains(err.Error(), "agenthail daemon start") {
				t.Fatalf("output=%q err=%v, want the daemon-start hint", output, err)
			}
			if loads := len(c.fake.Calls("thread/resume")) + len(c.fake.Calls("thread/fork")); loads != 0 || c.fake.Loaded(c.id) || c.r.QueueCount(c.id) != 0 {
				t.Fatalf("loads=%d loaded=%v queued=%d, want nothing loaded or queued", loads, c.fake.Loaded(c.id), c.r.QueueCount(c.id))
			}
		})
	}
}

func TestASendThatLoadsACodexThreadLoadsItThroughTheDaemonWithItsAgenthailServer(t *testing.T) {
	c := unloadedCodexThread(t)
	c.serveDaemon(t)
	if output, err := captureStdout(t, func() error { return c.app.Run([]string{"send", c.id, "hello"}) }); err != nil {
		t.Fatalf("output=%q err=%v", output, err)
	}
	bearer := agenthailBearer(c.fake.LoadedConfig(c.id))
	hash, live, err := c.r.LiveCredentialHash(c.id, time.Now())
	if bearer == "" || err != nil || !live || registry.HashSessionToken(bearer) != hash {
		t.Fatalf("config=%v live=%v err=%v, want the thread loaded with its live agenthail credential", c.fake.LoadedConfig(c.id), live, err)
	}
	if thread, _ := c.fake.Thread(c.id); len(thread.Turns) != 2 || !strings.HasSuffix(thread.Turns[1].User, "hello") {
		t.Fatalf("thread=%+v, want the message delivered once", thread)
	}
}

func TestAForkOfACodexThreadStartsThroughTheDaemonWithItsAgenthailServer(t *testing.T) {
	c := unloadedCodexThread(t)
	c.serveDaemon(t)
	if output, err := captureStdout(t, func() error { return c.app.Run([]string{"thread", "fork", c.id, "--alias", "forked"}) }); err != nil {
		t.Fatalf("output=%q err=%v", output, err)
	}
	forks := c.fake.Calls("thread/fork")
	if len(forks) != 1 {
		t.Fatalf("forks=%v, want one", forks)
	}
	config, _ := forks[0]["config"].(map[string]any)
	if agenthailBearer(config) == "" {
		t.Fatalf("the fork carried config %v, want the agenthail server", config)
	}
	if id, err := c.r.LookupAlias("forked"); err != nil || id == "" || id == c.id {
		t.Fatalf("alias forked=%q err=%v, want the fork named", id, err)
	}
}

func TestWithoutADaemonALoadedCodexThreadTakesASendButIsNotForked(t *testing.T) {
	c := unloadedCodexThread(t)
	c.fake.Load(c.id)
	if output, err := captureStdout(t, func() error { return c.app.Run([]string{"send", c.id, "hello"}) }); err != nil {
		t.Fatalf("output=%q err=%v, want the loaded thread to take the send where it is", output, err)
	}
	if thread, _ := c.fake.Thread(c.id); len(thread.Turns) != 2 {
		t.Fatalf("thread=%+v, want the message delivered", thread)
	}
	output, err := captureStdout(t, func() error { return c.app.Run([]string{"thread", "fork", c.id}) })
	if err == nil || !strings.Contains(err.Error(), "agenthail daemon start") || len(c.fake.Calls("thread/fork")) != 0 {
		t.Fatalf("output=%q err=%v forks=%d, want the fork refused before Codex is asked", output, err, len(c.fake.Calls("thread/fork")))
	}
}
