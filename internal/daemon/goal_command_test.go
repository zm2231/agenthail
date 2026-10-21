package daemon

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// goalCommandSurface sets goals by command, as Claude does: a peer-only
// session refuses one, and a run reports what the session answered.
type goalCommandSurface struct {
	*daemonSurface
	mu      sync.Mutex
	ran     []string
	runErr  error
	release chan struct{}
}

func (s *goalCommandSurface) GoalCommand(sess *surface.Session, objective string) (string, error) {
	if sess.Transport == "uds" {
		return "", surface.DeliveryTerminal(errors.New("the peer socket cannot run /goal"), surface.DeliveryInvalidRequest)
	}
	if objective == "" {
		return "/goal clear", nil
	}
	return "/goal " + objective, nil
}

func (s *goalCommandSurface) RunGoalCommand(ctx context.Context, _ *surface.Session, command string) error {
	if s.release != nil {
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ran = append(s.ran, command)
	return s.runErr
}

func (s *goalCommandSurface) commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ran...)
}

func goalCommandFixture(t *testing.T, status surface.SessionStatus) (*Daemon, *registry.Registry, *goalCommandSurface, surface.Session) {
	t.Helper()
	daemon, r, fake, _, _ := daemonFixture(t)
	session := surface.Session{ID: "claude", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "claude", Status: status}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	fake.kind = surface.KindClaude
	fake.sessions = map[string]surface.Session{session.ID: session}
	fake.observations[session.ID] = &surface.TurnObservation{Status: status}
	fake.caps = surface.Capabilities{Send: true, GoalSet: true, GoalStatus: true}
	adapter := &goalCommandSurface{daemonSurface: fake}
	daemon.Surfaces = []surface.Surface{adapter}
	return daemon, r, adapter, session
}

func TestDashboardHoldsAClaudeGoalUntilTheSessionIsIdle(t *testing.T) {
	daemon, r, adapter, session := goalCommandFixture(t, surface.StatusBusy)
	handler := dashboardRouter(daemon)
	response := serveDashboardRequest(handler, http.MethodPost, "/api/action", `{"action":"goal-set","sessionId":"claude","message":"  tests pass "}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"evidence":"queued"`) || !strings.Contains(response.Body.String(), "/goal tests pass will run when the target is idle") || r.QueueCount(session.ID) != 1 || len(adapter.commands()) != 0 {
		t.Fatalf("status=%d queue=%d ran=%v body=%s", response.Code, r.QueueCount(session.ID), adapter.commands(), response.Body.String())
	}
	for _, action := range []string{"goal-pause", "goal-resume", "goal-budget"} {
		if response := serveDashboardRequest(handler, http.MethodPost, "/api/action", `{"action":"`+action+`","sessionId":"claude","message":"10"}`); response.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", action, response.Code, response.Body.String())
		}
	}
	if response := serveDashboardRequest(handler, http.MethodPost, "/api/action", `{"action":"goal-set","sessionId":"claude","message":" "}`); response.Code != http.StatusBadRequest || r.QueueCount(session.ID) != 1 {
		t.Fatalf("empty objective status=%d queue=%d", response.Code, r.QueueCount(session.ID))
	}
}

func TestDashboardRefusesAGoalTheSessionCannotRunWithItsReason(t *testing.T) {
	daemon, r, adapter, session := goalCommandFixture(t, surface.StatusIdle)
	session.Transport = "uds"
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	adapter.sessions[session.ID] = session
	adapter.caps.GoalSet = false
	response := serveDashboardRequest(dashboardRouter(daemon), http.MethodPost, "/api/action", `{"action":"goal-clear","sessionId":"claude"}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "the peer socket cannot run /goal") || r.QueueCount(session.ID) != 0 {
		t.Fatalf("status=%d queue=%d body=%s", response.Code, r.QueueCount(session.ID), response.Body.String())
	}
}

func TestQueuedGoalRunsAtIdleBeforeTheMessagesBehindIt(t *testing.T) {
	daemon, r, adapter, session := goalCommandFixture(t, surface.StatusIdle)
	adapter.release = make(chan struct{})
	id, err := r.QueueGoal(session.ID, "/goal tests pass")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.QueueMessage(session.ID, "follow-up"); err != nil {
		t.Fatal(err)
	}
	daemon.scanAndRelay(context.Background())
	if len(adapter.sent) != 0 || r.QueueCount(session.ID) != 2 {
		t.Fatalf("while the goal runs sent=%v pending=%d", adapter.sent, r.QueueCount(session.ID))
	}
	close(adapter.release)
	deadline := time.Now().Add(time.Second)
	for r.QueueCount(session.ID) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	item, err := r.QueueItem(id, nil)
	if err != nil || item.Status != "delivered" || item.Evidence != surface.EvidenceDelivered || strings.Join(adapter.commands(), ",") != "/goal tests pass" {
		t.Fatalf("item=%+v ran=%v err=%v", item, adapter.commands(), err)
	}
	daemon.scanAndRelay(context.Background())
	if len(adapter.sent) != 1 || adapter.sent[0] != "follow-up" {
		t.Fatalf("after the goal sent=%v", adapter.sent)
	}
}

func TestQueuedGoalSettlesByWhatTheSessionAnswered(t *testing.T) {
	for name, test := range map[string]struct {
		err            error
		status, reason string
	}{
		"busy again": {surface.DeliveryUnavailable(errors.New("Claude is working")), "pending", "Claude is working"},
		"refused":    {surface.DeliveryTerminal(errors.New("Hooks are disabled by policy"), surface.DeliveryInvalidRequest), "dead", "Hooks are disabled by policy"},
		"unanswered": {surface.DeliveryOutcomeUnknown(errors.New("no outcome")), "dead", "no outcome"},
	} {
		t.Run(name, func(t *testing.T) {
			daemon, r, adapter, session := goalCommandFixture(t, surface.StatusIdle)
			adapter.runErr = test.err
			id, err := r.QueueGoal(session.ID, "/goal tests pass")
			if err != nil {
				t.Fatal(err)
			}
			daemon.scanAndRelay(context.Background())
			// The goal runs on a queue worker; an item is pending before it
			// runs too, so the outcome is read once the worker has settled it.
			daemon.queueWorkers.Wait()
			item, err := r.QueueItem(id, nil)
			if err != nil || item.Status != test.status || !strings.Contains(item.LastError, test.reason) || len(adapter.commands()) != 1 {
				t.Fatalf("item=%+v ran=%v err=%v", item, adapter.commands(), err)
			}
		})
	}
}
