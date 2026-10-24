package voice

import (
	"fmt"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/testkit"
)

func TestAToolCallReachesItsHandlerWithoutWaitingOutThePoll(t *testing.T) {
	f, p := fixture(t)
	p.arrived = make(chan struct{}, 1)
	f.service.SetProjects(gatedProjects{})
	connectFixture(t, f)
	p.mu.Lock()
	p.pending = append(p.pending, Batch{Cursor: Cursor{Source: "fixture", Position: 20}, Events: []Event{
		{Method: "item/tool/call", Params: map[string]any{"requestId": "read", "threadId": "operator", "namespace": "agenthail", "tool": "voice_projects", "arguments": map[string]any{}}},
	}})
	p.mu.Unlock()
	p.arrived <- struct{}{}
	testkit.Eventually(t, "the projects answer without a tick ending the poll", 5*time.Second, func() bool { return len(p.answers()) == 1 })
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lastWait != pollWait || pollWait != 5*time.Second {
		t.Fatalf("poll waited up to %v, bound %v", p.lastWait, pollWait)
	}
}

func TestASourceThatLostItsEventsIsAskedAgainOnlyOnTheNextTick(t *testing.T) {
	f, p := fixture(t)
	connectFixture(t, f)
	p.mu.Lock()
	p.lost = true
	before := p.polls
	p.mu.Unlock()
	f.ticks <- time.Time{}
	testkit.Eventually(t, "the lost poll", 5*time.Second, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.polls-before >= 1
	})
	time.Sleep(50 * time.Millisecond)
	p.mu.Lock()
	polls := p.polls - before
	p.mu.Unlock()
	if polls != 1 {
		t.Fatalf("a lost source was polled %d times on one tick", polls)
	}
	if got := f.View(); !got.Truncated {
		t.Fatalf("the lost events were not recorded: %+v", got)
	}
	f.ticks <- time.Time{}
	testkit.Eventually(t, "the poll after the next tick", 5*time.Second, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.polls-before == 2
	})
}

func TestAPollWaitsNoLongerThanTheCallsNextDeadline(t *testing.T) {
	f, p := fixture(t)
	connectFixture(t, f)
	apply(t, f, Action{Action: "heartbeat", AttemptID: "call-a"})
	waitForWait := func(want time.Duration) {
		t.Helper()
		testkit.Eventually(t, fmt.Sprintf("a poll bounded at %v", want), 5*time.Second, func() bool {
			p.mu.Lock()
			defer p.mu.Unlock()
			return p.lastWait == want
		})
	}
	f.advance(38 * time.Second)
	f.tick()
	waitForWait(2*time.Second + time.Millisecond)
	f.advance(2*time.Second + time.Millisecond)
	f.tick()
	if requests, _, _ := p.snapshot(); len(requests) != 2 || requests[1] != "thread/realtime/stop" {
		t.Fatalf("the heartbeat expiry did not request the hangup when due: %v", requests)
	}
	waitForWait(pollWait)
	f.advance(28 * time.Second)
	f.tick()
	waitForWait(2*time.Second + time.Millisecond)
	f.advance(2*time.Second + time.Millisecond)
	f.tick()
	if got := f.View(); got.Phase != "unknown" {
		t.Fatalf("the hangup give-up did not end the call when due: %+v", got)
	}
}
