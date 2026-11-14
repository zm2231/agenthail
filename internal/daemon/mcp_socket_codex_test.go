package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestMCPSocketPostHelper(t *testing.T) {
	path := os.Getenv("AGENTHAIL_TEST_POST_BODY")
	if path == "" {
		return
	}
	var body io.Reader
	if prefix, endless := strings.CutPrefix(path, "endless:"); endless {
		body = io.MultiReader(strings.NewReader(prefix), endlessPadding{})
	} else if file, err := os.Open(path); err == nil {
		body = file
	} else {
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://agenthail/mcp", io.NopCloser(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	socket := os.Getenv("AGENTHAIL_TEST_RELAY_SOCKET")
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	response, err := client.Do(request)
	if err != nil {
		fmt.Print("0\n" + err.Error())
		os.Exit(0)
	}
	answer, _ := io.ReadAll(response.Body)
	fmt.Printf("%d\n%s", response.StatusCode, answer)
	os.Exit(0)
}

type endlessPadding struct{}

func (endlessPadding) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}

func postFromCodexChild(t *testing.T, body []byte) (int, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "body")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return postPathFromCodexChild(t, path)
}

func postPathFromCodexChild(t *testing.T, path string) (int, string) {
	t.Helper()
	process := exec.Command(os.Args[0], "-test.run=^TestMCPSocketPostHelper$")
	process.Env = append(os.Environ(), "AGENTHAIL_TEST_POST_BODY="+path, "AGENTHAIL_TEST_RELAY_SOCKET="+MCPSocketPath())
	out, err := process.Output()
	if err != nil {
		t.Fatalf("post helper: %v", err)
	}
	status, answer, _ := strings.Cut(string(out), "\n")
	var code int
	_, _ = fmt.Sscan(status, &code)
	return code, answer
}

func TestACodexChildCannotOversizeOrBatchItsRequests(t *testing.T) {
	_, reg, codex := codexSocketDaemon(t)
	registerCodex(t, reg, "thread-a", t.TempDir())
	codex.load(t, "thread-a")
	call := func(arguments string) string {
		return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"agenthail_whoami","arguments":` + arguments + `,"_meta":{"threadId":"thread-a"}}}`
	}

	if status, answer := postPathFromCodexChild(t, `endless:{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"agenthail_whoami","_meta":{"threadId":"thread-a"},"arguments":{"pad":"`); status != http.StatusRequestEntityTooLarge {
		t.Fatalf("a call whose body never ends got %d: %.200s", status, answer)
	}
	if status, answer := postFromCodexChild(t, []byte("["+call("{}")+"]")); strings.Contains(answer, "still starting") || !strings.Contains(answer, "invalid_request") {
		t.Fatalf("a batched call got %d: %.400s", status, answer)
	}
	if status, answer := postFromCodexChild(t, []byte(call("{}"))); status != http.StatusOK || !bytes.Contains([]byte(answer), []byte(`"sessionId":"thread-a"`)) {
		t.Fatalf("a call after them got %d: %.400s", status, answer)
	}
}
