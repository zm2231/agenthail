// Package claudehosttest runs the real Claude host against the fake Claude
// CLI in claude-host/test, under a temporary home.
package claudehosttest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	for _, dir := range []string{f.Cwd, f.ClaudeDir} {
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
	env := append(os.Environ(), "FAKE_CLAUDE_DIR="+f.ClaudeDir, "FAKE_CLAUDE_RECORD=1", "HOME="+f.Home)
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
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return false
	}
	command := strings.TrimSpace(string(output))
	return f.isHost(command) || strings.Contains(command, "fake-claude.mjs")
}

func processes() map[int]string {
	output, err := exec.Command("ps", "-axo", "pid=,command=").Output()
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
