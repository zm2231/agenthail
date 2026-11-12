package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/simhost"
)

type simCommands struct {
	mu      sync.Mutex
	lines   []string
	noAXe   bool
	shotPNG []byte
	holdOn  string
	held    chan struct{}
	release chan struct{}
}

func (s *simCommands) hold(prefix string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.holdOn, s.held, s.release = prefix, make(chan struct{}), make(chan struct{})
}

func (s *simCommands) exec(_ context.Context, cmd simhost.Command) ([]byte, error) {
	line := strings.Join(append([]string{cmd.Name}, cmd.Args...), " ")
	s.mu.Lock()
	if s.holdOn != "" && strings.HasPrefix(line, s.holdOn) {
		held, release := s.held, s.release
		s.holdOn = ""
		s.mu.Unlock()
		close(held)
		<-release
		s.mu.Lock()
	}
	defer s.mu.Unlock()
	s.lines = append(s.lines, line)
	switch {
	case cmd.Name == "axe" && s.noAXe:
		return nil, &simhost.CommandError{Command: "axe", Err: errors.New("executable file not found in $PATH")}
	case strings.HasPrefix(line, "xcrun simctl list runtimes"):
		return []byte(`{"runtimes":[{"identifier":"com.apple.CoreSimulator.SimRuntime.iOS-27-0","isAvailable":true}]}`), nil
	case strings.HasPrefix(line, "xcrun simctl list devices"):
		return []byte(`{"devices":{"r":[{"udid":"U1","name":"Agenthail iPhone","state":"Booted"}]}}`), nil
	case strings.HasPrefix(line, "axe --version"):
		return []byte("1.8.0\n"), nil
	case strings.HasPrefix(line, "xcrun simctl io U1 screenshot"):
		return nil, os.WriteFile(cmd.Args[len(cmd.Args)-1], s.shotPNG, 0o600)
	}
	return nil, nil
}

func (s *simCommands) ran(prefix string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, line := range s.lines {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

type simFixture struct {
	d        *Daemon
	lockDir  string
	parent   *mcp.ClientSession
	child    *mcp.ClientSession
	parentID string
}

func newSimFixture(t *testing.T, commands *simCommands) *simFixture {
	t.Helper()
	d, _, _, parent, child := daemonFixture(t)
	lockDir := t.TempDir()
	d.SetSimulatorHost(simhost.New(simhost.Config{Exec: commands.exec, OutRoot: t.TempDir(), LockDir: lockDir, GOOS: "darwin"}))
	server := mcpServer(t, d)
	return &simFixture{d: d, lockDir: lockDir, parent: mcpSession(t, server, issueMCPToken(t, d, parent)), child: mcpSession(t, server, issueMCPToken(t, d, child)), parentID: parent.ID}
}

func simulate(t *testing.T, session *mcp.ClientSession, args map[string]any) map[string]any {
	t.Helper()
	out, refused := callMCP(t, session, "agenthail_simulator", args)
	if refused {
		t.Fatalf("agenthail_simulator %v: %v", args, out)
	}
	return out
}

func TestSimulatorToolLeasesADeviceToOneSessionAndArchivingReleasesIt(t *testing.T) {
	commands := &simCommands{}
	f := newSimFixture(t, commands)
	lease := simulate(t, f.parent, map[string]any{"action": "acquire", "kind": "iphone"})["lease"].(map[string]any)
	if lease["udid"] != "U1" || lease["outDir"] == "" {
		t.Fatalf("lease = %v", lease)
	}
	if code := refusalCode(t, f.child, "agenthail_simulator", map[string]any{"action": "acquire", "kind": "iphone"}); code != "busy" {
		t.Fatalf("second session acquire = %q", code)
	}
	if code := refusalCode(t, f.child, "agenthail_simulator", map[string]any{"action": "button", "button": "home"}); code != "not_held" {
		t.Fatalf("second session button = %q", code)
	}
	elsewhere := filepath.Join(t.TempDir(), "Elsewhere.app")
	if err := os.MkdirAll(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	if code := refusalCode(t, f.parent, "agenthail_simulator", map[string]any{"action": "install", "app": elsewhere}); code != "out_of_scope" {
		t.Fatalf("install from outside outDir = %q", code)
	}
	lock, err := os.OpenFile(filepath.Join(f.lockDir, "agenthail-simulator-U1.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
		t.Fatal("the lease does not hold the shared simulator lock")
	}
	if _, err := f.d.doSessionArchive("session-archive", f.parentID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "archiving releases the session's simulator", func() bool {
		return syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
	})
	if !commands.ran("xcrun simctl status_bar U1 clear") {
		t.Fatal("release did not reset the device")
	}
}

func TestSimulatorScreenshotSavesToAPathInTheSessionsFolders(t *testing.T) {
	commands := &simCommands{shotPNG: []byte("\x89PNG\r\n\x1a\nnot really")}
	f := newSimFixture(t, commands)
	simulate(t, f.parent, map[string]any{"action": "acquire", "kind": "iphone"})
	if code := refusalCode(t, f.parent, "agenthail_simulator", map[string]any{"action": "screenshot", "path": "/etc/shot.png"}); code != "out_of_scope" {
		t.Fatalf("screenshot outside the session's folders = %q", code)
	}
	shot := simulate(t, f.parent, map[string]any{"action": "screenshot"})
	if file, _ := shot["file"].(string); file == "" || shot["bytes"] != float64(len(commands.shotPNG)) {
		t.Fatalf("screenshot = %v", shot)
	}
}

func TestSimulatorToolSaysWhatTheHostLacks(t *testing.T) {
	f := newSimFixture(t, &simCommands{noAXe: true})
	out, refused := callMCP(t, f.parent, "agenthail_simulator", map[string]any{"action": "list"})
	if !refused || !strings.Contains(out["message"].(string), "AXe") || !strings.Contains(out["detail"].(map[string]any)["fix"].(string), "brew install") {
		t.Fatalf("list without AXe = %v", out)
	}
	bare, _, _, parent, _ := daemonFixture(t)
	if code := refusalCode(t, mcpSession(t, mcpServer(t, bare), issueMCPToken(t, bare, parent)), "agenthail_simulator", map[string]any{"action": "list"}); code != "unavailable" {
		t.Fatalf("list on a host with no simulators = %q", code)
	}
}
