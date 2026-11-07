package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testnode"
)

const listenerScript = `import net from 'node:net';
import fs from 'node:fs';
import { spawn } from 'node:child_process';
const [mode, dir] = process.argv.slice(2);
if (mode === 'detach') {
  spawn(process.execPath, [process.argv[1], 'orphan', dir], { detached: true, stdio: 'ignore' }).unref();
} else {
  const server = net.createServer(socket => { fs.appendFileSync(dir + '/accepted', 'x'); socket.destroy(); });
  server.listen(0, '127.0.0.1', () => fs.writeFileSync(dir + '/ready', server.address().port + ' ' + process.pid));
  setTimeout(() => process.exit(), 120_000).unref();
  if (mode === 'serve') process.stdin.on('end', () => process.exit()).resume();
}
`

type testListener struct {
	dir  string
	port int
	pid  int
}

func startListener(t *testing.T, script, mode string, env ...string) testListener {
	t.Helper()
	dir, err := os.MkdirTemp(filepath.Dir(script), "listener")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(testnode.Path(t), script, mode, dir)
	cmd.Env = append(surface.AgentEnv(os.Environ()), env...)
	if mode == "serve" {
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { stdin.Close() })
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	if mode == "detach" {
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	var ready []byte
	eventually(t, "the listener is ready", func() bool {
		ready, err = os.ReadFile(filepath.Join(dir, "ready"))
		return err == nil && len(ready) > 0
	})
	fields := strings.Fields(string(ready))
	port, _ := strconv.Atoi(fields[0])
	pid, _ := strconv.Atoi(fields[1])
	if mode == "detach" {
		t.Cleanup(func() { killIfRunning(pid, dir) })
	}
	return testListener{dir: dir, port: port, pid: pid}
}

func killIfRunning(pid int, dir string) {
	cmdline, err := exec.Command("ps", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
	if err == nil && strings.Contains(string(cmdline), dir) {
		if process, err := os.FindProcess(pid); err == nil {
			process.Kill()
		}
	}
}

func servedOn(t *testing.T, out map[string]any, port int) map[string]any {
	t.Helper()
	for _, entry := range out["servers"].([]any) {
		if server := entry.(map[string]any); int(server["port"].(float64)) == port {
			return server
		}
	}
	t.Fatalf("no server on port %d in %v", port, out)
	return nil
}

func TestServersNamesTheSessionThatStartedEachListenerWithoutConnecting(t *testing.T) {
	d, r, _, codex, _ := daemonFixture(t)
	claude := surface.Session{ID: "claude-other", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle}
	if err := r.RegisterSession(claude); err != nil {
		t.Fatal(err)
	}
	server := mcpServer(t, d)
	codexClient := mcpSession(t, server, issueMCPToken(t, d, codex))
	claudeClient := mcpSession(t, server, issueMCPToken(t, d, claude))

	script := filepath.Join(t.TempDir(), "listener.mjs")
	if err := os.WriteFile(script, []byte(listenerScript), 0o600); err != nil {
		t.Fatal(err)
	}
	tagged := startListener(t, script, "serve", "CODEX_THREAD_ID="+codex.ID)
	orphan := startListener(t, script, "detach", "CODEX_THREAD_ID="+codex.ID)
	untagged := startListener(t, script, "serve")
	claudes := startListener(t, script, "serve", "CLAUDE_CODE_SESSION_ID="+claude.ID)

	seenByCodex := browse(t, codexClient, map[string]any{"action": "servers"})
	for _, mine := range []testListener{tagged, orphan} {
		got := servedOn(t, seenByCodex, mine.port)
		if got["session"] != codex.ID || got["mine"] != true || int(got["pid"].(float64)) != mine.pid || got["command"] != filepath.Base(testnode.Path(t)) || got["address"] != "127.0.0.1" {
			t.Fatalf("listener started by the session = %v", got)
		}
	}
	if got := servedOn(t, seenByCodex, untagged.port); got["session"] != nil || got["mine"] != nil || int(got["pid"].(float64)) != untagged.pid {
		t.Fatalf("untagged listener = %v", got)
	}
	if got := servedOn(t, seenByCodex, claudes.port); got["session"] != nil || got["mine"] != nil {
		t.Fatalf("a session the caller cannot read is named: %v", got)
	}
	if got := servedOn(t, browse(t, claudeClient, map[string]any{"action": "servers"}), claudes.port); got["session"] != claude.ID || got["mine"] != true {
		t.Fatalf("Claude session's own listener = %v", got)
	}

	for _, listener := range []testListener{tagged, orphan, untagged, claudes} {
		if _, err := os.Stat(filepath.Join(listener.dir, "accepted")); !os.IsNotExist(err) {
			t.Fatalf("listing servers connected to port %d: %v", listener.port, err)
		}
	}
}
