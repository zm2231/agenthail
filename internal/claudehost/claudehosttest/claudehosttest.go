// Package claudehosttest runs the real Claude host against the fake Claude
// CLI in claude-host/test, under a temporary home.
package claudehosttest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost"
)

type Fixture struct {
	Home      string
	Cwd       string
	ClaudeDir string
}

type Turn struct {
	UUID     string          `json:"uuid"`
	Text     string          `json:"text"`
	Reply    string          `json:"reply"`
	Origin   json.RawMessage `json:"origin"`
	Priority string          `json:"priority"`
	PID      int             `json:"pid"`
}

// New makes a home short enough for unix socket paths. When home is
// non-empty it is used instead, so a test can share another fixture's home.
func New(t testing.TB, home string) *Fixture {
	t.Helper()
	if home == "" {
		dir, err := os.MkdirTemp("", "ch")
		if err != nil {
			t.Fatal(err)
		}
		home = dir
		t.Cleanup(func() { os.RemoveAll(dir) })
	}
	f := &Fixture{Home: home, Cwd: filepath.Join(home, "work"), ClaudeDir: filepath.Join(home, "fake-claude")}
	for _, dir := range []string{f.Cwd, f.ClaudeDir, f.reportDir()} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		if left := f.Kill(); len(left) > 0 {
			t.Errorf("processes for %s outlived the test: %v", f.Home, left)
		}
	})
	return f
}

// Manager starts hosts that report the given version and run the fake CLI,
// which writes its session record and serves its messaging socket.
func (f *Fixture) Manager(t testing.TB, version string) *claudehost.Manager {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	hostDir := filepath.Join(filepath.Dir(file), "..", "..", "..", "claude-host")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("the Claude host tests need node on PATH: %v", err)
	}
	if _, err := os.Stat(filepath.Join(hostDir, "node_modules", "@anthropic-ai", "claude-agent-sdk")); err != nil {
		t.Fatalf("install the host's dependencies first (npm ci --prefix claude-host): %v", err)
	}
	env := append(os.Environ(), "FAKE_CLAUDE_DIR="+f.ClaudeDir, "FAKE_CLAUDE_RECORD=1", "HOME="+f.Home,
		"NODE_OPTIONS=--report-on-signal --report-signal=SIGUSR2 --report-directory="+f.reportDir())
	return &claudehost.Manager{Home: f.Home, Launch: func() (claudehost.Launch, error) {
		return claudehost.Launch{
			Node:    node,
			Script:  filepath.Join(hostDir, "host.mjs"),
			Claude:  filepath.Join(hostDir, "test", "fake-claude.mjs"),
			Version: version,
			Env:     env,
		}, nil
	}}
}

// Start starts a host and returns its client.
func (f *Fixture) Start(t testing.TB) claudehost.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := f.Manager(t, "test").Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// Open opens a new session in the host and waits until discovery can see it.
func (f *Fixture) Open(t testing.TB, client claudehost.Client, sessionID string) claudehost.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := client.Open(ctx, claudehost.OpenRequest{SessionID: sessionID, Cwd: f.Cwd})
	if err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(f.Home, ".claude", "sessions", strconv.Itoa(session.PID)+".json")
	WaitFor(t, "the session record", func() bool { _, err := os.Stat(record); return err == nil })
	return session
}

// Turns is what the fake CLI ran for a session, in order.
func (f *Fixture) Turns(t testing.TB, sessionID string) []Turn {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.ClaudeDir, sessionID+".jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []Turn
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var turn Turn
		if err := json.Unmarshal([]byte(line), &turn); err != nil {
			t.Fatal(err)
		}
		out = append(out, turn)
	}
	return out
}

type Spawn struct {
	PID       int      `json:"pid"`
	SessionID string   `json:"sessionId"`
	Argv      []string `json:"argv"`
	Cwd       string   `json:"cwd"`
}

// Spawns are the fake CLIs the host started for a session, in order.
func (f *Fixture) Spawns(t testing.TB, sessionID string) []Spawn {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.ClaudeDir, "spawns.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []Spawn
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var spawn Spawn
		if err := json.Unmarshal([]byte(line), &spawn); err != nil {
			t.Fatal(err)
		}
		if spawn.SessionID == sessionID {
			out = append(out, spawn)
		}
	}
	return out
}

// Refuse makes the fake CLI exit with message before it starts the session.
func (f *Fixture) Refuse(t testing.TB, sessionID, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.ClaudeDir, "refuse-"+sessionID), []byte(message), 0600); err != nil {
		t.Fatal(err)
	}
}

// KillHost kills only the host, as a crash would. Its CLIs keep running
// until they finish their turn.
func (f *Fixture) KillHost(t testing.TB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client := claudehost.NewClient(f.Home)
	hello, err := client.Hello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = syscall.Kill(hello.PID, syscall.SIGKILL)
	WaitFor(t, "the host to exit", func() bool { return !Alive(hello.PID) })
}

// Kill stops every host serving this home and every fake CLI started for it,
// whether or not a host still answers, since hosts and CLIs run in their own
// sessions and nothing else stops them. It waits for them to exit and returns
// any that did not.
func (f *Fixture) Kill() []int {
	pids := append(f.HostPIDs(), f.cliPIDs()...)
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	var left []int
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(25 * time.Millisecond) {
		left = left[:0]
		for _, pid := range pids {
			if f.owns(pid) {
				left = append(left, pid)
			}
		}
		if len(left) == 0 || time.Now().After(deadline) {
			break
		}
	}
	for _, pid := range f.cliPIDs() {
		if !f.owns(pid) {
			_ = os.Remove(filepath.Join("/tmp/cc-socks", strconv.Itoa(pid)+".sock"))
		}
	}
	return left
}

// HostPIDs are the running host processes serving this home.
func (f *Fixture) HostPIDs() []int {
	var pids []int
	for pid, command := range processes() {
		if f.isHost(command) {
			pids = append(pids, pid)
		}
	}
	return pids
}

func (f *Fixture) isHost(command string) bool {
	return strings.Contains(command, "host.mjs") && strings.Contains(command, "--socket "+claudehost.SocketPath(f.Home))
}

// cliPIDs are the fake CLIs started for this home, from the spawn log they
// write when they start.
func (f *Fixture) cliPIDs() []int {
	data, err := os.ReadFile(filepath.Join(f.ClaudeDir, "spawns.jsonl"))
	if err != nil {
		return nil
	}
	var pids []int
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var spawn struct {
			PID int `json:"pid"`
		}
		if json.Unmarshal([]byte(line), &spawn) == nil && spawn.PID > 0 {
			pids = append(pids, spawn.PID)
		}
	}
	return pids
}

// owns reports whether pid is still a live host or fake CLI, so a reused pid
// is never mistaken for one.
func (f *Fixture) owns(pid int) bool {
	command := commandLine(pid)
	return f.isHost(command) || strings.Contains(command, "fake-claude.mjs")
}

func commandLine(pid int) string {
	output, err := stdout("ps", "-p", strconv.Itoa(pid), "-o", "command=")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func processes() map[int]string {
	output, err := stdout("ps", "-axo", "pid=,command=")
	if err != nil {
		return nil
	}
	out := map[int]string{}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if pid, err := strconv.Atoi(fields[0]); err == nil {
			out[pid] = strings.Join(fields[1:], " ")
		}
	}
	return out
}

// Diagnose describes the host serving this home, for a test that timed out
// waiting on it: the last error the test saw, the host process and its open
// files, the socket, the host log, and what the host is doing (Node's
// diagnostic report, plus a stack sample on macOS or the kernel wait channel
// on Linux). pid is the host the test expects; the hosts found serving this
// home are described too.
func (f *Fixture) Diagnose(pid int, lastErr error) string {
	var b strings.Builder
	fmt.Fprintf(&b, "last error: %v\n", lastErr)
	pids := f.HostPIDs()
	fmt.Fprintf(&b, "expected host pid %d alive=%v; hosts serving this home: %v\n", pid, Alive(pid), pids)
	if pid > 0 && !slices.Contains(pids, pid) {
		pids = append([]int{pid}, pids...)
	}
	socket := claudehost.SocketPath(f.Home)
	if info, err := os.Stat(socket); err != nil {
		fmt.Fprintf(&b, "socket %s: %v\n", socket, err)
	} else {
		fmt.Fprintf(&b, "socket %s: mode %s\n", socket, info.Mode())
	}
	for _, pid := range pids {
		id := strconv.Itoa(pid)
		section(&b, "ps "+id, "ps", "-p", id, "-o", "pid=,ppid=,stat=,etime=,wchan=,command=")
		section(&b, "lsof "+id, "lsof", "-nP", "-p", id)
		if !f.isHost(commandLine(pid)) {
			continue
		}
		fmt.Fprintf(&b, "--- node report %s\n%s\n", id, f.nodeReport(pid))
		if runtime.GOOS == "darwin" {
			path := filepath.Join(f.Home, "host-"+id+".sample.txt")
			_, err := command("sample", id, "1", "-file", path)
			sample, _ := os.ReadFile(path)
			sample, _, _ = bytes.Cut(sample, []byte("Binary Images:"))
			fmt.Fprintf(&b, "--- sample %s (err=%v)\n%s\n", id, err, head(sample, 32<<10))
		} else {
			for _, name := range []string{"status", "wchan", "stack"} {
				data, err := os.ReadFile(filepath.Join("/proc", id, name))
				fmt.Fprintf(&b, "--- /proc/%s/%s (err=%v)\n%s\n", id, name, err, tail(data, 4<<10))
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(claudehost.StateDir(f.Home), "host.log"))
	fmt.Fprintf(&b, "--- host.log (err=%v)\n%s\n", err, tail(data, 16<<10))
	return b.String()
}

func (f *Fixture) reportDir() string { return filepath.Join(f.Home, "node-reports") }

// nodeReport asks a host for Node's diagnostic report, which holds its
// JavaScript stack and open libuv handles, and returns those parts. Only a
// host started for this home handles the signal; it would end any other
// process, so the caller checks the pid is still that host. A stopped host
// cannot write one.
func (f *Fixture) nodeReport(pid int) string {
	if err := syscall.Kill(pid, syscall.SIGUSR2); err != nil {
		return fmt.Sprintf("signal: %v", err)
	}
	pattern := filepath.Join(f.reportDir(), fmt.Sprintf("report.*.%d.*.json", pid))
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		paths, _ := filepath.Glob(pattern)
		if len(paths) == 0 {
			continue
		}
		data, err := os.ReadFile(paths[len(paths)-1])
		if err != nil {
			return err.Error()
		}
		var report struct {
			JavaScriptStack json.RawMessage `json:"javascriptStack"`
			Libuv           json.RawMessage `json:"libuv"`
		}
		if err := json.Unmarshal(data, &report); err != nil {
			continue
		}
		out, _ := json.MarshalIndent(report, "", "  ")
		return string(head(out, 32<<10))
	}
	return "no report within 5s"
}

func section(b *strings.Builder, title, name string, args ...string) {
	output, err := command(name, args...)
	fmt.Fprintf(b, "--- %s (err=%v)\n%s\n", title, err, head(output, 16<<10))
}

func command(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func stdout(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

func head(data []byte, limit int) []byte {
	if len(data) > limit {
		return data[:limit]
	}
	return data
}

func tail(data []byte, limit int) []byte {
	if len(data) > limit {
		return data[len(data)-limit:]
	}
	return data
}

func WaitFor(t testing.TB, what string, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
		if done() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func Alive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }
