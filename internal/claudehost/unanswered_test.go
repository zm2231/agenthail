package claudehost_test

import (
	"os"
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

// stallingManager starts hosts whose event loop is blocked for block ms out of
// every block+gap ms once marker exists; with stopOnHangup the stall ends at
// SIGHUP, as a host busy on a long synchronous task eventually runs its
// signal handlers.
func stallingManager(t *testing.T, f *claudehosttest.Fixture, block, gap int, stopOnHangup bool) (*claudehost.Manager, string) {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "stall")
	script := filepath.Join(dir, "stall.cjs")
	hangup := ""
	if stopOnHangup {
		hangup = "process.on('SIGHUP', () => { stopped = true; });\n"
	}
	source := "const fs = require('fs');\nlet stopped = false;\n" + hangup +
		"setInterval(() => {\n  if (stopped || !fs.existsSync(" + strconv.Quote(marker) + ")) return;\n" +
		"  const end = Date.now() + " + strconv.Itoa(block) + ";\n  while (Date.now() < end) {}\n}, " + strconv.Itoa(gap) + ").unref();\n"
	if err := os.WriteFile(script, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	owner := claudehosttest.OwnerEnv()
	return f.Manager(t, "test", owner[1]+" --require="+script), marker
}

func TestEnsureRetiresAHostThatStoppedAnsweringAndKeepsItsSession(t *testing.T) {
	t.Cleanup(claudehost.SetUnansweredWaits(2*time.Second, 10*time.Second))
	f := claudehosttest.New(t, "")
	ctx := testContext(t)
	manager, marker := stallingManager(t, f, 3000, 10, true)
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
	if err := os.WriteFile(marker, nil, 0600); err != nil {
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
	client := f.Start(t)
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
	manager, marker := stallingManager(t, f, 500, 300, false)
	client, err := manager.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old, err := client.Hello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, nil, 0600); err != nil {
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
