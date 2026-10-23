package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

// scriptedStreamSurface runs one scripted stream per call.
type scriptedStreamSurface struct {
	*flakySeedSurface
	script func(call int32, ctx context.Context, onEvent func(surface.StreamEvent)) error
	calls  atomic.Int32
}

func (s *scriptedStreamSurface) Stream(ctx context.Context, _ *surface.Session, _ string, onEvent func(surface.StreamEvent), _ time.Duration) error {
	return s.script(s.calls.Add(1), ctx, onEvent)
}

func turnText(turn, text string) surface.StreamEvent {
	key := "managed:" + turn + ":text"
	return surface.StreamEvent{ID: key, ProviderKey: key, Version: uint64(len(text)), Operation: "upsert", TurnID: turn, Kind: "text", Role: "assistant", Text: text}
}

// followScripted follows a session through the script with a restart pause
// far longer than the test waits, so a source that pauses cannot pass.
func followScripted(t *testing.T, script func(call int32, ctx context.Context, onEvent func(surface.StreamEvent)) error) (*scriptedStreamSurface, func() []string) {
	t.Helper()
	previous := sessionStreamRestartPause
	sessionStreamRestartPause = time.Minute
	t.Cleanup(func() { sessionStreamRestartPause = previous })
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
	_, bodies := followScripted(t, func(call int32, ctx context.Context, onEvent func(surface.StreamEvent)) error {
		if call == 1 {
			onEvent(turnText("turn-1", "first reply"))
			return nil
		}
		onEvent(turnText("turn-2", "next reply"))
		<-ctx.Done()
		return ctx.Err()
	})
	testkit.Eventually(t, "the second stream", 5*time.Second, func() bool { return len(bodies()) == 2 })
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
			testkit.Eventually(t, "the stream calls before the pause", 5*time.Second, func() bool { return adapter.calls.Load() >= test.calls })
			time.Sleep(200 * time.Millisecond)
			if calls := adapter.calls.Load(); calls != test.calls {
				t.Fatalf("a stream that ended at once was called %d times before the pause ran out, want %d", calls, test.calls)
			}
		})
	}
}
