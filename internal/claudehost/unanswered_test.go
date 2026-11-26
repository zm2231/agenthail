package claudehost_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/testkit"
)

// stallingManager starts hosts that, once marker holds their pid, block their
// event loop for block ms out of every block+gap ms.
func stallingManager(t *testing.T, f *claudehosttest.Fixture, block, gap int) (*claudehost.Manager, string) {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "stall")
	return preloadedManager(t, f, "const fs = require('fs');\n"+
		"setInterval(() => {\n  let target = '';\n"+
		"  try { target = fs.readFileSync("+strconv.Quote(marker)+", 'utf8'); } catch {}\n"+
		"  if (target !== String(process.pid)) return;\n"+
		"  const end = Date.now() + "+strconv.Itoa(block)+";\n  while (Date.now() < end) {}\n}, "+strconv.Itoa(gap)+").unref();\n"), marker
}

// silentManager starts hosts that, once marker holds their pid, leave every
// new connection unanswered until they receive SIGHUP.
func silentManager(t *testing.T, f *claudehosttest.Fixture) (*claudehost.Manager, string) {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "silent")
	return preloadedManager(t, f, "const fs = require('fs');\nconst net = require('net');\n"+
		"let hungUp = false;\nprocess.on('SIGHUP', () => { hungUp = true; });\n"+
		"const silent = () => { try { return !hungUp && fs.readFileSync("+strconv.Quote(marker)+", 'utf8') === String(process.pid); } catch { return false; } };\n"+
		"const createServer = net.createServer;\n"+
		"net.createServer = (listener) => createServer((socket) => { if (!silent()) listener(socket); });\n"), marker
}

func preloadedManager(t *testing.T, f *claudehosttest.Fixture, source string) *claudehost.Manager {
	t.Helper()
	script := filepath.Join(t.TempDir(), "preload.cjs")
	if err := os.WriteFile(script, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	owner := claudehosttest.OwnerEnv()
	return f.Manager(t, "test", owner[1]+" --require="+script)
}

func TestEnsureRetiresAHostThatStoppedAnsweringAndKeepsItsSession(t *testing.T) {
	t.Cleanup(claudehost.SetUnansweredWaits(2*time.Second, 10*time.Second))
	f := claudehosttest.New(t, "")
	ctx := testContext(t)
	manager, marker := silentManager(t, f)
	client, err := manager.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old, err := client.Hello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.NewString()
	opened := f.Open(t, client, sessionID)
	if err := os.WriteFile(marker, []byte(strconv.Itoa(old.PID)), 0600); err != nil {
		t.Fatal(err)
	}

	client, err = manager.Ensure(ctx)
	if err != nil {
		t.Fatalf("Ensure with a silent host = %v, want it replaced", err)
	}
	hello, err := client.Hello(ctx)
	if err != nil || hello.PID == old.PID {
		t.Fatalf("hello after the replacement = %+v, %v; want a new host", hello, err)
	}
	retiring := client.Retiring()
	if len(retiring) != 1 {
		t.Fatalf("retiring hosts = %v, want the silent one", retiring)
	}
	if !claudehosttest.Alive(old.PID) || !claudehosttest.Alive(opened.PID) {
		t.Fatal("the replaced host or its Claude was stopped instead of retired")
	}
	listing, err := retiring[0].Own(ctx)
	if err != nil || len(listing.Sessions) != 1 || listing.Sessions[0].SessionID != sessionID {
		t.Fatalf("retiring host listing = %+v, %v; want it still serving %s", listing, err, sessionID)
	}
}

func TestEnsureKillsAHostThatCannotRetireAndKeepsItsClaudeRunning(t *testing.T) {
	t.Cleanup(claudehost.SetUnansweredWaits(2*time.Second, 2*time.Second))
	f := claudehosttest.New(t, "")
	ctx := testContext(t)
	client, err := f.Manager(t, "test", "FAKE_CLAUDE_OUTLIVES_INPUT=1").Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old, err := client.Hello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.NewString()
	opened := f.Open(t, client, sessionID)
	if err := syscall.Kill(old.PID, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(old.PID, syscall.SIGCONT) })

	client, err = f.Manager(t, "test").Ensure(ctx)
	if err != nil {
		t.Fatalf("Ensure with a stopped host = %v, want it replaced", err)
	}
	if hello, err := client.Hello(ctx); err != nil || hello.PID == old.PID {
		t.Fatalf("hello after the replacement = %+v, %v; want a new host", hello, err)
	}
	testkit.Eventually(t, "the stopped host to exit", 5*time.Second, func() bool { return !claudehosttest.Alive(old.PID) })
	if !claudehosttest.Alive(opened.PID) {
		t.Fatal("the Claude the stopped host ran was killed with it")
	}
	listing, err := client.List(ctx)
	if err != nil || len(listing.Orphans) != 1 || listing.Orphans[0].SessionID != sessionID {
		t.Fatalf("listing = %+v, %v; want the session held as an orphan", listing, err)
	}
}

func TestEnsureKeepsASlowHostThatStillAnswers(t *testing.T) {
	t.Cleanup(claudehost.SetUnansweredWaits(2*time.Second, 2*time.Second))
	f := claudehosttest.New(t, "")
	ctx := testContext(t)
	manager, marker := stallingManager(t, f, 500, 300)
	client, err := manager.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old, err := client.Hello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(strconv.Itoa(old.PID)), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		client, err := manager.Ensure(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if hello, err := client.Hello(ctx); err != nil || hello.PID != old.PID {
			t.Fatalf("hello from a slow host = %+v, %v; want pid %d kept", hello, err, old.PID)
		}
	}
	if retiring := claudehost.NewClient(f.Home).Retiring(); len(retiring) != 0 {
		t.Fatalf("retiring hosts = %v, want the slow host kept", retiring)
	}
}

func TestASecondDaemonWaitsForAHostStillStarting(t *testing.T) {
	t.Cleanup(claudehost.SetUnansweredWaits(time.Second, time.Second))
	f := claudehosttest.New(t, "")
	ctx := testContext(t)
	script := filepath.Join(t.TempDir(), "slow-start.cjs")
	if err := os.WriteFile(script, []byte("if (process.argv.includes('--socket')) { const end = Date.now() + 3000; while (Date.now() < end) {} }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	owner := claudehosttest.OwnerEnv()
	pids := make(chan int, 2)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			client, err := f.Manager(t, "test", owner[1]+" --require="+script).Ensure(ctx)
			if err == nil {
				var hello claudehost.Hello
				hello, err = client.Hello(ctx)
				pids <- hello.PID
			}
			errs <- err
		}()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("Ensure while the host starts = %v, want it to wait for the host", err)
		}
	}
	if first, second := <-pids, <-pids; first != second {
		t.Fatalf("hosts = %d and %d, want one", first, second)
	}
	if retiring := claudehost.NewClient(f.Home).Retiring(); len(retiring) != 0 {
		t.Fatalf("retiring hosts = %v, want none", retiring)
	}
}

func TestReplacementLeavesAReusedPIDAlone(t *testing.T) {
	f := claudehosttest.New(t, "")
	socket := claudehost.SocketPath(f.Home)
	other := exec.Command("sh", "-c", "sleep 30; :", "sh", "--socket", socket, "--state")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _ = other.Wait() })
	dir := filepath.Join(f.Home, ".agenthail", "claude-host")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"pid":`+strconv.Itoa(other.Process.Pid)+`}`), 0600); err != nil {
		t.Fatal(err)
	}
	manager := f.Manager(t, "test")
	if claudehost.SignalHost(manager, other.Process.Pid, "Thu Jan  1 00:00:00 1970", syscall.SIGKILL) {
		t.Fatal("a pid with another start time was signalled")
	}
	if !claudehosttest.Alive(other.Process.Pid) {
		t.Fatal("the process now holding the pid was killed")
	}
}
