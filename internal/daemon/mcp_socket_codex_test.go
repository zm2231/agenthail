package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/testkit"
)

type codexProcess struct {
	locks string
	held  map[string]*os.File
}

func codexSocketDaemon(t *testing.T) (*Daemon, *registry.Registry, *codexProcess) {
	t.Helper()
	d, reg, _, _ := mcpSocketDaemon(t)
	home := t.TempDir()
	d.Surfaces = append(d.Surfaces, surfaces.NewCodexIn(surfaces.CodexHome{Codex: home, State: filepath.Join(home, "state")}, ""))
	locks := filepath.Join(home, "thread-writer-locks")
	if err := os.MkdirAll(locks, 0o700); err != nil {
		t.Fatal(err)
	}
	process := &codexProcess{locks: locks, held: map[string]*os.File{}}
	t.Cleanup(func() {
		for _, file := range process.held {
			_ = file.Close()
		}
	})
	return d, reg, process
}

func (p *codexProcess) load(t *testing.T, thread string) {
	t.Helper()
	file, err := os.OpenFile(filepath.Join(p.locks, thread+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	p.held[thread] = file
}

func (p *codexProcess) unload(thread string) {
	_ = p.held[thread].Close()
	delete(p.held, thread)
}

func registerCodex(t *testing.T, reg *registry.Registry, id, cwd string) {
	t.Helper()
	if err := reg.RegisterSession(surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Cwd: cwd}); err != nil {
		t.Fatal(err)
	}
}

func startCodexRelay(t *testing.T, throughShell bool) *mcp.ClientSession {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	if err := os.WriteFile(ready, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	process := exec.Command(os.Args[0], "-test.run=^TestMCPStdioRelayHelper$")
	if throughShell {
		process = exec.Command("sh", "-c", `"$0" -test.run='^TestMCPStdioRelayHelper$'; :`, os.Args[0])
	}
	process.Env = append(os.Environ(), "AGENTHAIL_TEST_RELAY_READY="+ready, "AGENTHAIL_TEST_RELAY_SOCKET="+MCPSocketPath())
	stdin, err := process.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := process.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = process.Process.Kill()
		_ = process.Wait()
	})
	client, err := mcp.NewClient(&mcp.Implementation{Name: "codex-mcp-client", Version: "test"}, nil).Connect(context.Background(), &mcp.IOTransport{Reader: stdout, Writer: stdin}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func codexWhoami(t *testing.T, client *mcp.ClientSession, thread string) (sessionID, refused string) {
	t.Helper()
	params := &mcp.CallToolParams{Name: "agenthail_whoami"}
	if thread != "" {
		params.Meta = mcp.Meta{"threadId": thread, "sessionId": thread}
	}
	result, err := client.CallTool(context.Background(), params)
	if err != nil {
		return "", err.Error()
	}
	view, _ := json.Marshal(result.StructuredContent)
	var answer struct {
		SessionID string `json:"sessionId"`
		Code      string `json:"code"`
	}
	_ = json.Unmarshal(view, &answer)
	if result.IsError {
		return "", answer.Code
	}
	return answer.SessionID, ""
}

func TestACodexToolCallActsAsTheThreadItNamesOnlyWhileItsCodexProcessHoldsIt(t *testing.T) {
	d, reg, codex := codexSocketDaemon(t)
	folder := t.TempDir()
	for _, thread := range []string{"thread-a", "thread-b", "thread-c"} {
		registerCodex(t, reg, thread, folder)
	}
	codex.load(t, "thread-a")
	codex.load(t, "thread-b")

	if n := len(listedToolNames(t, startCodexRelay(t, true))); n != 0 {
		t.Fatalf("a relay below a shell, not started by Codex itself, listed %d tools", n)
	}
	relay := startCodexRelay(t, false)
	testkit.Eventually(t, "the Codex relay lists the tools", 10*time.Second, func() bool { return len(listedToolNames(t, relay)) > 0 })
	for _, thread := range []string{"thread-a", "thread-b"} {
		if id, refused := codexWhoami(t, relay, thread); id != thread {
			t.Fatalf("a call naming %s acted as %q (refused %q)", thread, id, refused)
		}
	}
	for thread, why := range map[string]string{"thread-c": "a thread its Codex process has not loaded", "": "no thread"} {
		if id, refused := codexWhoami(t, relay, thread); refused != "unauthenticated" {
			t.Fatalf("a call naming %s acted as %q (refused %q)", why, id, refused)
		}
	}
	codex.unload("thread-b")
	if id, refused := codexWhoami(t, relay, "thread-b"); refused != "unauthenticated" {
		t.Fatalf("a thread its Codex process let go of still acted, as %q", id)
	}
	if err := reg.ArchiveSession("thread-a", time.Now()); err != nil {
		t.Fatal(err)
	}
	if id, refused := codexWhoami(t, relay, "thread-a"); refused != "unauthenticated" {
		t.Fatalf("an archived thread still acted, as %q", id)
	}

	codex.load(t, "thread-c")
	if _, _, err := d.addProject(context.Background(), folder, "notes"); err != nil {
		t.Fatal(err)
	}
	session, err := reg.Session("thread-c")
	if err != nil {
		t.Fatal(err)
	}
	project, err := d.sessionProject(*session)
	if err != nil || project == nil {
		t.Fatalf("project = %v, %v", project, err)
	}
	if _, err := d.doProjectAction(context.Background(), principal{}, projectRequest{Action: "project-setting", ProjectID: project.ID, Setting: registry.ProjectSettingMCP, Value: registry.ProjectMCPOff}); err != nil {
		t.Fatal(err)
	}
	if id, refused := codexWhoami(t, relay, "thread-c"); refused != "tools_off" {
		t.Fatalf("with the project's tools off a Codex thread acted as %q (refused %q)", id, refused)
	}
}
