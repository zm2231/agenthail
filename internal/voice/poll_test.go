package voice

import (
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
