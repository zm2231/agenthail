package claudehost

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
)

type hostFixture struct {
	home      string
	cwd       string
	claudeDir string
}

func newHostFixture(t *testing.T) hostFixture {
	t.Helper()
	home, err := os.MkdirTemp("", "ch")
	if err != nil {
		t.Fatal(err)
	}
	f := hostFixture{home: home, cwd: filepath.Join(home, "work"), claudeDir: filepath.Join(home, "fake-claude")}
	for _, dir := range []string{f.cwd, f.claudeDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if hello, err := NewClient(home).Hello(ctx); err == nil {
			_ = syscall.Kill(hello.PID, syscall.SIGKILL)
		}
		os.RemoveAll(home)
	})
	return f
}

func (f hostFixture) manager(t *testing.T, version string) *Manager {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	hostDir := filepath.Join(filepath.Dir(file), "..", "..", "claude-host")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("the Claude host tests need node on PATH: %v", err)
	}
	if _, err := os.Stat(filepath.Join(hostDir, "node_modules", "@anthropic-ai", "claude-agent-sdk")); err != nil {
		t.Fatalf("install the host's dependencies first (npm ci --prefix claude-host): %v", err)
	}
	return &Manager{Home: f.home, Launch: func() (Launch, error) {
		return Launch{
			Node:    node,
			Script:  filepath.Join(hostDir, "host.mjs"),
			Claude:  filepath.Join(hostDir, "test", "fake-claude.mjs"),
			Version: version,
			Env:     append(os.Environ(), "FAKE_CLAUDE_DIR="+f.claudeDir),
		}, nil
	}}
}

func testContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if done() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func transcriptUUIDs(t *testing.T, f hostFixture, sessionID string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.claudeDir, sessionID+".jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		start := strings.Index(line, `"uuid":"`) + len(`"uuid":"`)
		out = append(out, line[start:start+36])
	}
	return out
}

func TestEnsureStartsOneHostAndARestartedDaemonAttachesToIt(t *testing.T) {
	f := newHostFixture(t)
	ctx := testContext(t)
	first := f.manager(t, "1.0.0")
	client, err := first.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hello, err := client.Hello(ctx)
	if err != nil || hello.Protocol != Protocol || hello.HostVersion != "1.0.0" {
		t.Fatalf("hello = %+v, %v", hello, err)
	}
	sessionID := uuid.NewString()
	opened, err := client.Open(ctx, OpenRequest{SessionID: sessionID, Cwd: f.cwd})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Ensure(ctx); err != nil {
		t.Fatal(err)
	}

	restarted := f.manager(t, "1.0.0")
	client, err = restarted.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	again, err := client.Hello(ctx)
	if err != nil || again.PID != hello.PID {
		t.Fatalf("restarted daemon reached host %+v, %v; want the running host pid %d", again, err, hello.PID)
	}
	if _, err := restarted.lock(); !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatalf("host lock while the host runs: %v, want EWOULDBLOCK", err)
	}
	listing, err := client.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Sessions) != 1 || listing.Sessions[0].SessionID != sessionID || listing.Sessions[0].PID != opened.PID {
		t.Fatalf("sessions after daemon restart = %+v, want %s on pid %d", listing.Sessions, sessionID, opened.PID)
	}
	message := uuid.NewString()
	if result, err := client.Send(ctx, SendRequest{SessionID: sessionID, UUID: message, Text: "after restart", Origin: &Origin{Kind: "human"}}); err != nil || !result.Accepted || result.Duplicate {
		t.Fatalf("send = %+v, %v", result, err)
	}
	waitFor(t, "the turn", func() bool { return slices.Equal(transcriptUUIDs(t, f, sessionID), []string{message}) })
}

func TestEnsureReplacesAHostOfAnotherVersionOnceItIsIdle(t *testing.T) {
	f := newHostFixture(t)
	ctx := testContext(t)
	old := f.manager(t, "1.0.0")
	client, err := old.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	oldHello, _ := client.Hello(ctx)
	sessionID := uuid.NewString()
	opened, err := client.Open(ctx, OpenRequest{SessionID: sessionID, Cwd: f.cwd})
	if err != nil {
		t.Fatal(err)
	}
	running := uuid.NewString()
	if _, err := client.Send(ctx, SendRequest{SessionID: sessionID, UUID: running, Text: "SLOW 800"}); err != nil {
		t.Fatal(err)
	}

	updated := f.manager(t, "1.1.0")
	if _, err := updated.Ensure(ctx); !errors.Is(err, ErrReplacing) {
		t.Fatalf("Ensure with a running older host = %v, want ErrReplacing", err)
	}
	if _, err := client.Open(ctx, OpenRequest{SessionID: uuid.NewString(), Cwd: f.cwd}); !IsCode(err, "draining") {
		t.Fatalf("open on a draining host = %v, want draining", err)
	}
	waitFor(t, "the old host to finish the turn and exit", func() bool { return !processAlive(oldHello.PID) && !processAlive(opened.PID) })
	if got := transcriptUUIDs(t, f, sessionID); !slices.Equal(got, []string{running}) {
		t.Fatalf("turns = %v, want the running turn finished", got)
	}

	client, err = updated.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hello, err := client.Hello(ctx)
	if err != nil || hello.HostVersion != "1.1.0" || hello.PID == oldHello.PID {
		t.Fatalf("hello after replacement = %+v, %v", hello, err)
	}
	if _, err := client.Open(ctx, OpenRequest{Resume: sessionID, Cwd: f.cwd}); err != nil {
		t.Fatal(err)
	}
	next := uuid.NewString()
	if _, err := client.Send(ctx, SendRequest{SessionID: sessionID, UUID: running, Text: "SLOW 800"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Send(ctx, SendRequest{SessionID: sessionID, UUID: next, Text: "next"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the next turn", func() bool { return slices.Equal(transcriptUUIDs(t, f, sessionID), []string{running, next}) })
}

func TestClientDistinguishesNoHostFromARefusal(t *testing.T) {
	f := newHostFixture(t)
	ctx := testContext(t)
	if _, err := NewClient(f.home).Hello(ctx); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("hello without a host = %v, want ErrNotRunning", err)
	}
	client, err := f.manager(t, "1.0.0").Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Send(ctx, SendRequest{SessionID: uuid.NewString(), UUID: uuid.NewString(), Text: "x"})
	if !IsCode(err, "not_open") {
		t.Fatalf("send to a session the host does not run = %v, want not_open", err)
	}
}

func TestSubscribeStreamsEventsBoundToTheMessage(t *testing.T) {
	f := newHostFixture(t)
	ctx := testContext(t)
	client, err := f.manager(t, "1.0.0").Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.NewString()
	if _, err := client.Open(ctx, OpenRequest{SessionID: sessionID, Cwd: f.cwd}); err != nil {
		t.Fatal(err)
	}
	message := uuid.NewString()
	events := make(chan Event, 64)
	subscribed, stop := context.WithCancel(ctx)
	defer stop()
	go func() { _, _ = client.Subscribe(subscribed, 0, func(e Event) { events <- e }) }()
	if _, err := client.Send(ctx, SendRequest{SessionID: sessionID, UUID: message, Text: "TOOL"}); err != nil {
		t.Fatal(err)
	}
	var denied, result bool
	for !denied || !result {
		select {
		case e := <-events:
			denied = denied || (e.Kind == "permission_denied" && e.Tool == "Bash" && e.SessionID == sessionID)
			result = result || (e.Kind == "sdk" && strings.Contains(string(e.Message), `"type":"result"`) && strings.Contains(string(e.Message), message))
		case <-ctx.Done():
			t.Fatalf("events: denied=%v result=%v", denied, result)
		}
	}
}
