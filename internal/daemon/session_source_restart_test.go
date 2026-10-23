package daemon

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// scriptedStreamSurface runs one scripted stream per call and keeps when
// each call started and returned.
type scriptedStreamSurface struct {
	*flakySeedSurface
	script func(call int32, ctx context.Context, onEvent func(surface.StreamEvent)) error
	calls  atomic.Int32
	mu     sync.Mutex
	starts []time.Time
	ends   []time.Time
}

func (s *scriptedStreamSurface) Stream(ctx context.Context, _ *surface.Session, _ string, onEvent func(surface.StreamEvent), _ time.Duration) error {
	call := s.calls.Add(1)
	s.mu.Lock()
	s.starts = append(s.starts, time.Now())
	s.mu.Unlock()
	err := s.script(call, ctx, onEvent)
	s.mu.Lock()
	s.ends = append(s.ends, time.Now())
	s.mu.Unlock()
	return err
}

func (s *scriptedStreamSurface) times() (starts, ends []time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time(nil), s.starts...), append([]time.Time(nil), s.ends...)
}

func turnText(turn, text string) surface.StreamEvent {
	key := "managed:" + turn + ":text"
	return surface.StreamEvent{ID: key, ProviderKey: key, Version: uint64(len(text)), Operation: "upsert", TurnID: turn, Kind: "text", Role: "assistant", Text: text}
}

func followScripted(t *testing.T, script func(call int32, ctx context.Context, onEvent func(surface.StreamEvent)) error) (*scriptedStreamSurface, func() []string) {
	t.Helper()
	_, reg, fake, from, _ := daemonFixture(t)
	adapter := &scriptedStreamSurface{flakySeedSurface: &flakySeedSurface{daemonSurface: fake}, script: script}
	adapter.caps.Stream = true
	subscription, err := newTestSourceManager(t, reg).subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(subscription.Cancel)
	return adapter, func() []string {
		var bodies []string
		for _, payload := range journalPayloads(t, reg, from.ID) {
			if payload.Kind == "text" {
				bodies = append(bodies, payload.Body)
			}
		}
		return bodies
	}
}

func TestALiveSourceFollowsTheNextTurnAsSoonAsAStreamEndsWithItsTurn(t *testing.T) {
	adapter, bodies := followScripted(t, func(call int32, ctx context.Context, onEvent func(surface.StreamEvent)) error {
		if call == 1 {
			onEvent(turnText("turn-1", "first reply"))
			return nil
		}
		onEvent(turnText("turn-2", "next reply"))
		<-ctx.Done()
		return ctx.Err()
	})
	waitFor(t, 5*time.Second, func() bool { return len(bodies()) == 2 })
	starts, ends := adapter.times()
	if gap := starts[1].Sub(ends[0]); gap >= 200*time.Millisecond {
		t.Fatalf("the source waited %v after a turn's stream ended before reading the next turn", gap)
	}
}

func TestALiveSourceWaitsBeforeStreamingAgainWhenAStreamEndsAtOnceWithNothingNew(t *testing.T) {
	for _, test := range []struct {
		name  string
		event bool
		calls int32
	}{
		// An empty window ends at once; it is not asked again until the pause.
		{name: "empty window", calls: 1},
		// A window that only repeats what the journal holds is no progress.
		{name: "repeated event", event: true, calls: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter, _ := followScripted(t, func(_ int32, _ context.Context, onEvent func(surface.StreamEvent)) error {
				if test.event {
					onEvent(turnText("turn-1", "same reply"))
				}
				return surface.ErrStreamWindow
			})
			waitFor(t, 5*time.Second, func() bool { return adapter.calls.Load() >= test.calls })
			time.Sleep(sessionStreamRestartPause / 2)
			if calls := adapter.calls.Load(); calls != test.calls {
				t.Fatalf("a stream that ended at once was called %d times within half the pause, want %d", calls, test.calls)
			}
		})
	}
}
