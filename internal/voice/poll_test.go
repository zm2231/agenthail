package voice

import (
	"fmt"
	"testing"
	"time"
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
	sent := time.Now()
	p.arrived <- struct{}{}
	waitFor(t, "the projects answer", func() bool { return len(p.answers()) == 1 })
	if elapsed := time.Since(sent); elapsed >= 100*time.Millisecond {
		t.Fatalf("the tool call took %v to reach its handler", elapsed)
	}
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
	waitFor(t, "the lost poll", func() bool {
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
	waitFor(t, "the poll after the next tick", func() bool {
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
		waitFor(t, fmt.Sprintf("a poll bounded at %v", want), func() bool {
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
