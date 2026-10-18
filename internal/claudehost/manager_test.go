package claudehost_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
)

func testContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func uuids(turns []claudehosttest.Turn) []string {
	var out []string
	for _, turn := range turns {
		out = append(out, turn.UUID)
	}
	return out
}

func lockHost(home string) error {
	file, err := os.Open(filepath.Join(home, ".agenthail", "claude-host", "lock"))
	if err != nil {
		return err
	}
	defer file.Close()
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func TestEnsureStartsOneHostAndARestartedDaemonAttachesToIt(t *testing.T) {
	f := claudehosttest.New(t, "")
	ctx := testContext(t)
	first := f.Manager(t, "1.0.0")
	client, err := first.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hello, err := client.Hello(ctx)
	if err != nil || hello.Protocol != claudehost.Protocol || hello.HostVersion != "1.0.0" {
		t.Fatalf("hello = %+v, %v", hello, err)
	}
	sessionID := uuid.NewString()
	opened, err := client.Open(ctx, claudehost.OpenRequest{SessionID: sessionID, Cwd: f.Cwd})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Ensure(ctx); err != nil {
		t.Fatal(err)
	}

	restarted := f.Manager(t, "1.0.0")
	client, err = restarted.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	again, err := client.Hello(ctx)
	if err != nil || again.PID != hello.PID {
		t.Fatalf("restarted daemon reached host %+v, %v; want the running host pid %d", again, err, hello.PID)
	}
	if err := lockHost(f.Home); !errors.Is(err, syscall.EWOULDBLOCK) {
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
	if result, err := client.Send(ctx, claudehost.SendRequest{SessionID: sessionID, UUID: message, Text: "after restart", Origin: &claudehost.Origin{Kind: "human"}}); err != nil || !result.Accepted || result.Duplicate {
		t.Fatalf("send = %+v, %v", result, err)
	}
	claudehosttest.WaitFor(t, "the turn", func() bool { return slices.Equal(uuids(f.Turns(t, sessionID)), []string{message}) })
}

func TestDaemonsEnsuringAtOnceShareOneHost(t *testing.T) {
	f := claudehosttest.New(t, "")
	ctx := testContext(t)
	pids := make(chan int, 8)
	var wg sync.WaitGroup
	for range cap(pids) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client, err := f.Manager(t, "1.0.0").Ensure(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			hello, err := client.Hello(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			pids <- hello.PID
		}()
	}
	wg.Wait()
	close(pids)
	seen := map[int]bool{}
	for pid := range pids {
		seen[pid] = true
	}
	if len(seen) != 1 {
		t.Fatalf("hosts = %v, want one", seen)
	}
	if starts := strings.Count(hostLog(t, f.Home), "listening"); starts != 1 {
		t.Fatalf("host started %d times", starts)
	}
}

func TestEnsureStartsANewHostAfterOneCrashed(t *testing.T) {
	f := claudehosttest.New(t, "")
	ctx := testContext(t)
	client := f.Start(t)
	first, err := client.Hello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.KillHost(t)
	if _, err := os.Stat(claudehost.SocketPath(f.Home)); err != nil {
		t.Fatalf("a crashed host leaves its socket file: %v", err)
	}
	client, err = f.Manager(t, "test").Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := client.Hello(ctx); err != nil || again.PID == first.PID {
		t.Fatalf("hello after the crash = %+v, %v; want a new host", again, err)
	}
}

func TestEnsureTakesTheLockWhenItsHolderExitsBeforeItsHostAnswers(t *testing.T) {
	f := claudehosttest.New(t, "")
	ctx := testContext(t)
	dir := filepath.Join(f.Home, ".agenthail", "claude-host")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	holder, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	taken := make(chan struct{})
	var once sync.Once
	t.Cleanup(claudehost.OnLockTaken(func() { once.Do(func() { close(taken) }) }))
	done := make(chan error, 1)
	go func() {
		_, err := f.Manager(t, "test").Ensure(ctx)
		done <- err
	}()
	<-taken
	holder.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Ensure did not take the released lock")
	}
	if starts := strings.Count(hostLog(t, f.Home), "listening"); starts != 1 {
		t.Fatalf("host started %d times", starts)
	}
}

func hostLog(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".agenthail", "claude-host", "host.log"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestEnsureReplacesAHostOfAnotherVersionOnceItIsIdle(t *testing.T) {
	f := claudehosttest.New(t, "")
	ctx := testContext(t)
	old := f.Manager(t, "1.0.0")
	client, err := old.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	oldHello, _ := client.Hello(ctx)
	sessionID := uuid.NewString()
	opened, err := client.Open(ctx, claudehost.OpenRequest{SessionID: sessionID, Cwd: f.Cwd})
	if err != nil {
		t.Fatal(err)
	}
	running := uuid.NewString()
	if _, err := client.Send(ctx, claudehost.SendRequest{SessionID: sessionID, UUID: running, Text: "SLOW 800"}); err != nil {
		t.Fatal(err)
	}

	updated := f.Manager(t, "1.1.0")
	if _, err := updated.Ensure(ctx); !errors.Is(err, claudehost.ErrReplacing) {
		t.Fatalf("Ensure with a running older host = %v, want claudehost.ErrReplacing", err)
	}
	if _, err := client.Open(ctx, claudehost.OpenRequest{SessionID: uuid.NewString(), Cwd: f.Cwd}); !claudehost.IsCode(err, "draining") {
		t.Fatalf("open on a draining host = %v, want draining", err)
	}
	claudehosttest.WaitFor(t, "the old host to finish the turn and exit", func() bool { return !claudehosttest.Alive(oldHello.PID) && !claudehosttest.Alive(opened.PID) })
	if got := uuids(f.Turns(t, sessionID)); !slices.Equal(got, []string{running}) {
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
	if _, err := client.Open(ctx, claudehost.OpenRequest{Resume: sessionID, Cwd: f.Cwd}); err != nil {
		t.Fatal(err)
	}
	next := uuid.NewString()
	if _, err := client.Send(ctx, claudehost.SendRequest{SessionID: sessionID, UUID: running, Text: "SLOW 800"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Send(ctx, claudehost.SendRequest{SessionID: sessionID, UUID: next, Text: "next"}); err != nil {
		t.Fatal(err)
	}
	claudehosttest.WaitFor(t, "the next turn", func() bool { return slices.Equal(uuids(f.Turns(t, sessionID)), []string{running, next}) })
}

func TestClientDistinguishesNoHostFromARefusal(t *testing.T) {
	f := claudehosttest.New(t, "")
	ctx := testContext(t)
	if _, err := claudehost.NewClient(f.Home).Hello(ctx); !errors.Is(err, claudehost.ErrNotRunning) {
		t.Fatalf("hello without a host = %v, want claudehost.ErrNotRunning", err)
	}
	client, err := f.Manager(t, "1.0.0").Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Send(ctx, claudehost.SendRequest{SessionID: uuid.NewString(), UUID: uuid.NewString(), Text: "x"})
	if !claudehost.IsCode(err, "not_open") {
		t.Fatalf("send to a session the host does not run = %v, want not_open", err)
	}
}

func TestSubscribeStreamsEventsBoundToTheMessage(t *testing.T) {
	f := claudehosttest.New(t, "")
	ctx := testContext(t)
	client, err := f.Manager(t, "1.0.0").Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.NewString()
	if _, err := client.Open(ctx, claudehost.OpenRequest{SessionID: sessionID, Cwd: f.Cwd}); err != nil {
		t.Fatal(err)
	}
	message := uuid.NewString()
	events := make(chan claudehost.Event, 64)
	subscribed, stop := context.WithCancel(ctx)
	defer stop()
	go func() { _, _ = client.Subscribe(subscribed, 0, func(e claudehost.Event) { events <- e }) }()
	if _, err := client.Send(ctx, claudehost.SendRequest{SessionID: sessionID, UUID: message, Text: "TOOL"}); err != nil {
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
