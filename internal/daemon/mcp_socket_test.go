package daemon

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/mcpstdio"
	"github.com/zm2231/agenthail/internal/procstart"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestMCPStdioRelayHelper(t *testing.T) {
	ready := os.Getenv("AGENTHAIL_TEST_RELAY_READY")
	if ready == "" {
		return
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(ready); err == nil {
			break
		}
	}
	_ = mcpstdio.Run(context.Background(), os.Getenv("AGENTHAIL_TEST_RELAY_SOCKET"), "test", os.Stdin, os.Stdout)
	os.Exit(0)
}

type relayedSession struct {
	client      *mcp.ClientSession
	record      string
	process     *exec.Cmd
	listChanged chan struct{}
}

func startRelayedSession(t *testing.T, root, sessionID string) *relayedSession {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	process := exec.Command("sh", "-c", `"$0" -test.run='^TestMCPStdioRelayHelper$'; :`, os.Args[0])
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
	session := &relayedSession{record: filepath.Join(root, "sessions", strconv.Itoa(process.Process.Pid)+".json"), process: process, listChanged: make(chan struct{}, 1)}
	session.write(t, sessionID)
	if err := os.WriteFile(ready, nil, 0600); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "test"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
			select {
			case session.listChanged <- struct{}{}:
			default:
			}
		},
	})
	session.client, err = client.Connect(context.Background(), &mcp.IOTransport{Reader: stdout, Writer: stdin}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.client.Close() })
	return session
}

func (s *relayedSession) write(t *testing.T, sessionID string) {
	t.Helper()
	s.writeFor(t, s.process.Process.Pid, sessionID, true)
}

func (s *relayedSession) writeFor(t *testing.T, pid int, sessionID string, byThatProcess bool) {
	t.Helper()
	record := map[string]any{"pid": pid, "sessionId": sessionID, "kind": "interactive", "entrypoint": "cli"}
	switch {
	case runtime.GOOS == "linux" && byThatProcess:
		ticks, err := procstart.Ticks(pid)
		if err != nil {
			t.Fatal(err)
		}
		record["procStart"] = ticks
	case runtime.GOOS == "linux":
		record["procStart"] = "1"
	case byThatProcess:
		record["startedAt"] = time.Now().UnixMilli()
	default:
		record["startedAt"] = 1
	}
	data, _ := json.Marshal(record)
	if err := os.MkdirAll(filepath.Dir(s.record), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.record, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func (s *relayedSession) whoami(t *testing.T) (string, bool) {
	t.Helper()
	result, err := s.client.CallTool(context.Background(), &mcp.CallToolParams{Name: "agenthail_whoami"})
	if err != nil || result.IsError {
		return "", false
	}
	view, _ := json.Marshal(result.StructuredContent)
	var whoami struct {
		SessionID  string            `json:"sessionId"`
		Credential map[string]string `json:"credential"`
	}
	_ = json.Unmarshal(view, &whoami)
	if whoami.Credential != nil {
		t.Fatalf("a session named by its process was shown a credential: %v", whoami.Credential)
	}
	return whoami.SessionID, true
}

func (s *relayedSession) toolCount(t *testing.T) int {
	t.Helper()
	listed, err := s.client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return len(listed.Tools)
}

// shortSocketHome keeps ~/.agenthail/mcp.sock inside macOS's 104-byte
// socket path limit, which a test binary's temporary home can exceed.
func shortSocketHome(t *testing.T) {
	t.Helper()
	base := ""
	if runtime.GOOS == "darwin" {
		base = "/tmp"
	}
	home, err := os.MkdirTemp(base, "ah")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
}

func mcpSocketDaemon(t *testing.T) (*Daemon, *registry.Registry, string, func()) {
	t.Helper()
	shortSocketHome(t)
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	reg, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	d := newTestDaemon(t, reg, []surface.Surface{surfaces.NewClaude(nil, os.Getenv("HOME"))})
	stop, err := d.startMCPSocket()
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	stopOnce := func() { once.Do(stop) }
	t.Cleanup(stopOnce)
	return d, reg, root, stopOnce
}

func registerClaude(t *testing.T, reg *registry.Registry, id string) {
	t.Helper()
	if err := reg.RegisterSession(surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle, Cwd: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
}

func TestASessionsOwnStdioServerActsAsTheSessionItsProcessHoldsNow(t *testing.T) {
	d, reg, root, stop := mcpSocketDaemon(t)
	registerClaude(t, reg, "claude-two")
	go func() {
		time.Sleep(300 * time.Millisecond)
		registerClaude(t, reg, "claude-one")
	}()
	session := startRelayedSession(t, root, "claude-one")
	testkit.Eventually(t, "the session's tools are listed", 10*time.Second, func() bool { return session.toolCount(t) > 0 })
	if id, ok := session.whoami(t); !ok || id != "claude-one" {
		t.Fatalf("whoami = %q, %v", id, ok)
	}
	session.write(t, "claude-two")
	if id, ok := session.whoami(t); !ok || id != "claude-two" {
		t.Fatalf("after /clear the record names claude-two, but whoami = %q, %v", id, ok)
	}
	stop()
	restarted, err := d.startMCPSocket()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted)
	if id, ok := session.whoami(t); !ok || id != "claude-two" {
		t.Fatalf("after the daemon's socket restarts, whoami = %q, %v", id, ok)
	}
	if err := reg.ArchiveSession("claude-two", time.Now()); err != nil {
		t.Fatal(err)
	}
	if id, ok := session.whoami(t); ok {
		t.Fatalf("an archived session still acted, as %q", id)
	}
}

func TestASessionStartedWhileTheDaemonIsDownGetsItsToolsWhenItComesUp(t *testing.T) {
	d, reg, root, stop := mcpSocketDaemon(t)
	registerClaude(t, reg, "claude-one")
	stop()
	session := startRelayedSession(t, root, "claude-one")
	if n := session.toolCount(t); n != 0 {
		t.Fatalf("with the daemon down the session listed %d tools", n)
	}
	restarted, err := d.startMCPSocket()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted)
	select {
	case <-session.listChanged:
	case <-time.After(15 * time.Second):
		t.Fatal("the session was never told its tools changed")
	}
	if id, ok := session.whoami(t); !ok || id != "claude-one" {
		t.Fatalf("whoami = %q, %v", id, ok)
	}
}

func TestAProcessThatIsNoSessionGetsNoTools(t *testing.T) {
	_, reg, root, _ := mcpSocketDaemon(t)
	registerClaude(t, reg, "claude-one")
	stranger := socketClient(t)
	listed, err := stranger.ListTools(context.Background(), nil)
	if err != nil || len(listed.Tools) != 0 {
		t.Fatalf("a process with no session among its ancestors listed %d tools (%v)", len(listed.Tools), err)
	}
	if result, err := stranger.CallTool(context.Background(), &mcp.CallToolParams{Name: "agenthail_whoami"}); err == nil && !result.IsError {
		t.Fatalf("a process with no session called a tool: %+v", result)
	}
	parent := &relayedSession{record: filepath.Join(root, "sessions", strconv.Itoa(os.Getppid())+".json")}
	parent.writeFor(t, os.Getppid(), "claude-one", false)
	if listed, err := socketClient(t).ListTools(context.Background(), nil); err != nil || len(listed.Tools) != 0 {
		t.Fatalf("a record its process did not write gave %d tools (%v)", len(listed.Tools), err)
	}
	parent.writeFor(t, os.Getppid(), "claude-one", true)
	if listed, err := socketClient(t).ListTools(context.Background(), nil); err != nil || len(listed.Tools) == 0 {
		t.Fatalf("the parent's own record gave %d tools (%v)", len(listed.Tools), err)
	}
}

func socketClient(t *testing.T) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "stranger", Version: "test"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: "http://agenthail/mcp", DisableStandaloneSSE: true, HTTPClient: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", MCPSocketPath())
		},
	}}}
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

type processOwner struct {
	*daemonSurface
	pid int
	id  string
}

func (p processOwner) ProcessSession(pid int) string {
	if pid == p.pid {
		return p.id
	}
	return ""
}

type threadOwner struct {
	*daemonSurface
	pid int
}

func (o threadOwner) ProcessThreads(pid int) []string {
	if pid == o.pid {
		return []string{"thread"}
	}
	return nil
}

func TestASessionNamedByItsProcessGetsExtensionToolsToo(t *testing.T) {
	home := extensionTestHome(t)
	writeToolExtension(t, home, "wide", `{"label": "Wide", "capabilities": ["tools"]}`, toolServer)
	f := startExtensionDaemon(t, home, extensionDaemonOptions{})
	f.awaitAllLoaded(t, "wide")
	f.approve(t, "wide")
	f.d.Surfaces = append(f.d.Surfaces, processOwner{daemonSurface: f.fake, pid: os.Getppid(), id: "from"})
	shortSocketHome(t)
	stop, err := f.d.startMCPSocket()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	names := listedToolNames(t, socketClient(t))
	if !slices.Contains(names, "ext_wide_peek") || !slices.Contains(names, "agenthail_send") {
		t.Fatalf("a session named by its process lists %v", names)
	}
	f.d.Surfaces = append(f.d.Surfaces[:len(f.d.Surfaces)-1], threadOwner{daemonSurface: f.fake, pid: os.Getppid()})
	names = listedToolNames(t, socketClient(t))
	if !slices.Contains(names, "ext_wide_peek") || !slices.Contains(names, "agenthail_send") {
		t.Fatalf("a Codex process's server, before any call names its thread, lists %v", names)
	}
}

func TestAnMCPSocketPathTooLongForThePlatformIsRefusedSayingWhatToChange(t *testing.T) {
	home := filepath.Join(t.TempDir(), strings.Repeat("h", 120))
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	reg, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	d := newTestDaemon(t, reg, nil)
	stop, err := d.startMCPSocket()
	if err == nil {
		stop()
		t.Fatal("the daemon listened on a socket path the platform cannot hold")
	}
	if !strings.Contains(err.Error(), MCPSocketPath()) || !strings.Contains(err.Error(), "shorter home directory") {
		t.Fatalf("the refusal does not name the path and what to change: %v", err)
	}
}

func TestAProjectWithTheToolsOffGivesItsTerminalSessionsNoneUntilTurnedBackOn(t *testing.T) {
	d, reg, root, _ := mcpSocketDaemon(t)
	folder := t.TempDir()
	if _, _, err := d.addProject(context.Background(), folder, "notes"); err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterSession(surface.Session{ID: "claude-one", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle, Cwd: folder}); err != nil {
		t.Fatal(err)
	}
	parent := &relayedSession{record: filepath.Join(root, "sessions", strconv.Itoa(os.Getppid())+".json")}
	parent.writeFor(t, os.Getppid(), "claude-one", true)
	if names := listedToolNames(t, socketClient(t)); !slices.Contains(names, "agenthail_send") {
		t.Fatalf("before the setting the session lists %v", names)
	}
	session, err := reg.Session("claude-one")
	if err != nil {
		t.Fatal(err)
	}
	project, err := d.sessionProject(*session)
	if err != nil || project == nil {
		t.Fatalf("project = %v, %v", project, err)
	}
	set := func(value string) {
		t.Helper()
		if _, err := d.doProjectAction(context.Background(), principal{}, projectRequest{Action: "project-setting", ProjectID: project.ID, Setting: registry.ProjectSettingMCP, Value: value}); err != nil {
			t.Fatal(err)
		}
	}
	set(registry.ProjectMCPOff)
	client := socketClient(t)
	if names := listedToolNames(t, client); len(names) != 0 {
		t.Fatalf("with the project's tools off the session lists %v", names)
	}
	if result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "agenthail_whoami"}); err == nil && !result.IsError {
		t.Fatalf("with the project's tools off the session called a tool: %+v", result)
	}
	set(registry.ProjectMCPOn)
	if names := listedToolNames(t, client); !slices.Contains(names, "agenthail_send") {
		t.Fatalf("with the tools back on the session lists %v", names)
	}
}
