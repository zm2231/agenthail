package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func hostRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func hostedSession(t *testing.T, claude *surfaces.Claude, id string) surface.Session {
	t.Helper()
	var found surface.Session
	claudehosttest.WaitFor(t, "the host session to be listed", func() bool {
		sessions, err := claude.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, session := range sessions {
			if session.ID == id && session.RuntimeKind == surface.RuntimeClaudeHost {
				found = session
				return true
			}
		}
		return false
	})
	return found
}

func turnOrigin(t *testing.T, turn claudehosttest.Turn) string {
	t.Helper()
	var origin struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(turn.Origin, &origin); err != nil {
		t.Fatal(err)
	}
	return origin.Kind
}

func TestAPersonsMessageToAHostSessionIsDeliveredUnderItsIntentUUID(t *testing.T) {
	fixture := claudehosttest.New(t, "")
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	claude := surfaces.NewClaude(nil, fixture.Home)
	session := hostedSession(t, claude, id)
	r := hostRegistry(t)
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	d := New(r, []surface.Surface{claude})
	ctx := context.Background()

	receipt, err := d.dispatcher().DeliverWithOptions(ctx, claude, &session, "sent now", "", surface.SendOptions{Author: surface.AuthorPerson})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := r.DeliveryIntent(receipt.DeliveryID)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Evidence != surface.EvidenceDelivered || receipt.TurnID != intent.UUID {
		t.Fatalf("receipt = %+v, want delivered with turn %s", receipt, intent.UUID)
	}

	queued, err := r.RecordDeliveryIntent(registry.DeliveryIntentInput{SenderSessionID: registry.OperatorSessionID, TargetSessionID: id, Message: "sent from the queue", Status: registry.DeliveryIntentSubmitted, Evidence: surface.EvidenceSubmitted})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.QueueDeliveryUsingIntent(queued.ID, "sent from the queue", "", surface.SendOptions{Author: surface.AuthorPerson}); err != nil {
		t.Fatal(err)
	}
	d.drainMessageQueue(ctx, claude, &session)

	claudehosttest.WaitFor(t, "both turns", func() bool { return len(fixture.Turns(t, id)) == 2 })
	turns := fixture.Turns(t, id)
	for i, want := range []struct{ uuid, text string }{{intent.UUID, "sent now"}, {queued.UUID, "sent from the queue"}} {
		if turns[i].UUID != want.uuid || turns[i].Text != want.text || turnOrigin(t, turns[i]) != "human" {
			t.Fatalf("turn %d = %+v, want a human turn %q under %s", i, turns[i], want.text, want.uuid)
		}
	}
}

func TestAStoppedDaemonLeavesNoClaudeHostSupervisorRunning(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var returned atomic.Bool
	manager := &claudehost.Manager{Home: t.TempDir(), Launch: func() (claudehost.Launch, error) {
		close(entered)
		<-release
		returned.Store(true)
		return claudehost.Launch{}, errors.New("no Claude host in this test")
	}}
	d := New(hostRegistry(t), nil)
	d.SetClaudeHost(manager)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = d.Run(ctx)
	}()
	<-entered
	cancel()
	select {
	case <-done:
		close(release)
		t.Fatal("Run returned while its Claude host supervisor was still running")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	<-done
	if !returned.Load() {
		t.Fatal("Run returned before its Claude host supervisor finished")
	}
}

func TestARestartedDaemonAttachesToTheRunningHostAndKeepsItsSessions(t *testing.T) {
	fixture := claudehosttest.New(t, "")
	r := hostRegistry(t)
	run := func(manager *claudehost.Manager) (stop func()) {
		d := New(r, nil)
		d.SetClaudeHost(manager)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = d.Run(ctx)
		}()
		return func() {
			cancel()
			<-done
		}
	}
	client := claudehost.NewClient(fixture.Home)
	hello := func(expected int) claudehost.Hello {
		t.Helper()
		var lastErr error
		for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			got, err := client.Hello(ctx)
			cancel()
			if err == nil {
				return got
			}
			lastErr = err
		}
		t.Fatalf("timed out waiting for the host\n%s", fixture.Diagnose(expected, lastErr))
		return claudehost.Hello{}
	}

	stop := run(fixture.Manager(t, "1.0.0"))
	first := hello(0)
	failf := func(format string, args ...any) {
		t.Helper()
		t.Fatalf(format+"\n%s", append(args, fixture.Diagnose(first.PID, nil))...)
	}
	id := uuid.NewString()
	opened := fixture.Open(t, client, id)
	stop()

	if !claudehosttest.Alive(first.PID) || !claudehosttest.Alive(opened.PID) {
		failf("the host and its session outlive the daemon")
	}
	if alone := hello(first.PID); alone.PID != first.PID {
		failf("with no daemon running, host %d answered, want %d", alone.PID, first.PID)
	}
	stop = run(fixture.Manager(t, "1.0.0"))
	defer stop()
	time.Sleep(300 * time.Millisecond)
	if again := hello(first.PID); again.PID != first.PID {
		failf("restarted daemon reached host %d, want the running host %d", again.PID, first.PID)
	}
	lock, err := os.Open(filepath.Join(fixture.Home, ".agenthail", "claude-host", "lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != syscall.EWOULDBLOCK {
		failf("host lock = %v, want held by the one running host", err)
	}
	listing, err := client.List(context.Background())
	if err != nil {
		failf("list: %v", err)
	}
	if len(listing.Sessions) != 1 || listing.Sessions[0].SessionID != id || listing.Sessions[0].PID != opened.PID {
		failf("sessions = %+v, want %s still on pid %d", listing.Sessions, id, opened.PID)
	}
	log, err := os.ReadFile(filepath.Join(fixture.Home, ".agenthail", "claude-host", "host.log"))
	if err != nil {
		t.Fatal(err)
	}
	if starts := strings.Count(string(log), "listening"); starts != 1 {
		failf("host started %d times", starts)
	}
}
