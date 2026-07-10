package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type daemonSurface struct {
	sessions     map[string]surface.Session
	observations map[string]*surface.TurnObservation
	accepted     bool
	sent         []string
	models       []string
	listCalls    atomic.Int32
	resolveCalls atomic.Int32
	rejectBusy   bool
	sendErr      error
}

func (*daemonSurface) Name() surface.SurfaceKind { return surface.KindCodex }
func (f *daemonSurface) List(context.Context) ([]surface.Session, error) {
	f.listCalls.Add(1)
	result := make([]surface.Session, 0, len(f.sessions))
	for _, session := range f.sessions {
		result = append(result, session)
	}
	return result, nil
}
func (f *daemonSurface) Resolve(_ context.Context, id string) (*surface.Session, error) {
	f.resolveCalls.Add(1)
	session, ok := f.sessions[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return &session, nil
}
func (f *daemonSurface) Observe(_ context.Context, session *surface.Session) (*surface.TurnObservation, error) {
	return f.observations[session.ID], nil
}
func (f *daemonSurface) Send(_ context.Context, session *surface.Session, message string) (*surface.SendResult, error) {
	if f.rejectBusy && session.Status == surface.StatusBusy {
		return &surface.SendResult{Accepted: false}, nil
	}
	f.sent = append(f.sent, message)
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	return &surface.SendResult{UUID: "turn", Accepted: f.accepted}, nil
}
func (f *daemonSurface) SendWithOptions(_ context.Context, session *surface.Session, message string, options surface.SendOptions) (*surface.SendResult, error) {
	if f.rejectBusy && session.Status == surface.StatusBusy {
		return &surface.SendResult{Accepted: false}, nil
	}
	f.sent = append(f.sent, message)
	f.models = append(f.models, options.Model)
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	return &surface.SendResult{UUID: "turn", Accepted: f.accepted}, nil
}
func (*daemonSurface) Reply(context.Context, *surface.Session, int) (*surface.ReplyResult, error) {
	return nil, nil
}
func (*daemonSurface) Tail(context.Context, *surface.Session, int) ([]surface.Exchange, error) {
	return nil, nil
}
func (*daemonSurface) Stream(context.Context, *surface.Session, string, func(surface.StreamEvent), time.Duration) error {
	return nil
}
func (*daemonSurface) GoalSet(context.Context, *surface.Session, string) error { return nil }
func (*daemonSurface) GoalClear(context.Context, *surface.Session) error       { return nil }
func (*daemonSurface) GoalGet(context.Context, *surface.Session) (*surface.GoalState, error) {
	return nil, nil
}
func (*daemonSurface) Compact(context.Context, *surface.Session) error { return nil }
func (*daemonSurface) Model(context.Context, *surface.Session, string) (string, error) {
	return "", nil
}
func (*daemonSurface) Interrupt(context.Context, *surface.Session) error     { return nil }
func (*daemonSurface) Steer(context.Context, *surface.Session, string) error { return nil }
func (*daemonSurface) Capabilities() surface.Capabilities                    { return surface.Capabilities{} }

func daemonFixture(t *testing.T) (*Daemon, *registry.Registry, *daemonSurface, surface.Session, surface.Session) {
	t.Helper()
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	from := surface.Session{ID: "from", Surface: surface.KindCodex, Status: surface.StatusIdle}
	to := surface.Session{ID: "to", Surface: surface.KindCodex, Status: surface.StatusIdle}
	if err := r.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession(to); err != nil {
		t.Fatal(err)
	}
	fake := &daemonSurface{sessions: map[string]surface.Session{"from": from, "to": to}, observations: map[string]*surface.TurnObservation{}, accepted: true}
	return New(r, []surface.Surface{fake}), r, fake, from, to
}

func TestObservationBaselinesThenRelaysOnceAndQueuesBusyTarget(t *testing.T) {
	daemon, r, fake, from, _ := daemonFixture(t)
	if _, err := r.AddRoute("from", "to", ".*"); err != nil {
		t.Fatal(err)
	}
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-1", Reply: &surface.ReplyResult{Text: "one", Done: true}}
	daemon.observeSession(context.Background(), fake, &from)
	if len(fake.sent) != 0 {
		t.Fatalf("baseline relayed: %v", fake.sent)
	}

	fake.accepted = false
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-2", Reply: &surface.ReplyResult{Text: "same", Done: true}}
	daemon.observeSession(context.Background(), fake, &from)
	if len(fake.sent) != 0 || r.QueueCount("to") != 1 {
		t.Fatalf("sent=%v pending=%d", fake.sent, r.QueueCount("to"))
	}
	daemon.observeSession(context.Background(), fake, &from)
	if len(fake.sent) != 0 || r.QueueCount("to") != 1 {
		t.Fatalf("duplicate: sent=%v pending=%d", fake.sent, r.QueueCount("to"))
	}
}

func TestScanObservesOnlyWatchedSessionsWithoutDiscovery(t *testing.T) {
	daemon, r, fake, _, _ := daemonFixture(t)
	if _, err := r.AddRoute("from", "to", ".*"); err != nil {
		t.Fatal(err)
	}
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "from-turn"}
	fake.observations["to"] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "to-turn"}

	daemon.scanAndRelay(context.Background())

	if got := fake.listCalls.Load(); got != 0 {
		t.Fatalf("daemon performed %d full surface discovery call(s)", got)
	}
	if got := fake.resolveCalls.Load(); got != 0 {
		t.Fatalf("daemon performed %d redundant resolve call(s)", got)
	}
}

func TestScanDrainsClaudeQueueWithObservedIdleStatus(t *testing.T) {
	daemon, r, fake, _, to := daemonFixture(t)
	to.Status = surface.StatusBusy
	if err := r.RegisterSession(to); err != nil {
		t.Fatal(err)
	}
	if err := r.QueueMessage(to.ID, "deliver after idle"); err != nil {
		t.Fatal(err)
	}
	fake.rejectBusy = true
	fake.observations[to.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "done"}

	daemon.scanAndRelay(context.Background())

	if r.QueueCount(to.ID) != 0 || len(fake.sent) != 1 || fake.sent[0] != "deliver after idle" {
		t.Fatalf("pending=%d sent=%v", r.QueueCount(to.ID), fake.sent)
	}
}

func TestFailedCompletionIsNotRelayed(t *testing.T) {
	daemon, r, fake, from, _ := daemonFixture(t)
	if _, err := r.AddRoute("from", "to", ".*"); err != nil {
		t.Fatal(err)
	}
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "baseline", Reply: &surface.ReplyResult{Text: "ok", Done: true}}
	daemon.observeSession(context.Background(), fake, &from)
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "failed", Reply: &surface.ReplyResult{Text: "partial", Done: true, Error: "turn failed"}}
	daemon.observeSession(context.Background(), fake, &from)
	if r.QueueCount("to") != 0 || len(fake.sent) != 0 {
		t.Fatalf("failed completion relayed: pending=%d sent=%v", r.QueueCount("to"), fake.sent)
	}
}

func TestRelayIsPersistedWhileDestinationIsUnavailable(t *testing.T) {
	daemon, r, fake, from, to := daemonFixture(t)
	if _, err := r.AddRoute("from", "to", ".*"); err != nil {
		t.Fatal(err)
	}
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "baseline", Reply: &surface.ReplyResult{Text: "old", Done: true}}
	daemon.observeSession(context.Background(), fake, &from)
	delete(fake.sessions, "to")
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "next", Reply: &surface.ReplyResult{Text: "handoff", Done: true}}
	daemon.observeSession(context.Background(), fake, &from)
	if r.QueueCount("to") != 1 {
		t.Fatalf("relay was not durably queued: pending=%d", r.QueueCount("to"))
	}
	fake.sessions["to"] = to
	fake.accepted = true
	daemon.drainMessageQueue(context.Background(), fake, &to)
	if r.QueueCount("to") != 0 || len(fake.sent) != 1 {
		t.Fatalf("pending=%d sent=%v", r.QueueCount("to"), fake.sent)
	}
}

func TestOutboxOnlyAcknowledgesAcceptedDelivery(t *testing.T) {
	daemon, r, fake, _, to := daemonFixture(t)
	if err := r.QueueMessage("to", "wait"); err != nil {
		t.Fatal(err)
	}
	fake.accepted = false
	daemon.drainMessageQueue(context.Background(), fake, &to)
	if r.QueueCount("to") != 1 {
		t.Fatal("rejected message disappeared")
	}

	daemon2, r2, fake2, _, to2 := daemonFixture(t)
	if err := r2.QueueMessage("to", "deliver"); err != nil {
		t.Fatal(err)
	}
	fake2.accepted = true
	daemon2.drainMessageQueue(context.Background(), fake2, &to2)
	if r2.QueueCount("to") != 0 || len(fake2.sent) != 1 {
		t.Fatalf("pending=%d sent=%v", r2.QueueCount("to"), fake2.sent)
	}
}

func TestOutboxPreservesQueuedModelSelection(t *testing.T) {
	daemon, r, fake, _, to := daemonFixture(t)
	if _, err := r.QueueMessageWithOptions("to", "modelled", "", surface.SendOptions{Model: "sonnet"}); err != nil {
		t.Fatal(err)
	}
	daemon.drainMessageQueue(context.Background(), fake, &to)
	if len(fake.models) != 1 || fake.models[0] != "sonnet" || r.QueueCount("to") != 0 {
		t.Fatalf("models=%v pending=%d", fake.models, r.QueueCount("to"))
	}
}

func TestOutboxDeadLettersUnknownDeliveryWithoutAutomaticRetry(t *testing.T) {
	daemon, r, fake, _, to := daemonFixture(t)
	if err := r.QueueMessage("to", "maybe delivered"); err != nil {
		t.Fatal(err)
	}
	fake.sendErr = surface.DeliveryOutcomeUnknown(context.DeadlineExceeded)
	daemon.drainMessageQueue(context.Background(), fake, &to)
	daemon.drainMessageQueue(context.Background(), fake, &to)
	if len(fake.sent) != 1 {
		t.Fatalf("ambiguous delivery retried %d times", len(fake.sent))
	}
	rows, err := r.ListQueue(false)
	if err != nil || len(rows) != 1 || rows[0].Status != "dead" || !strings.Contains(rows[0].LastError, "outcome is unknown") {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}
