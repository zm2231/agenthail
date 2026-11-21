package surfacetest

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface/transcript"
)

const wakeBound = time.Second

func AwaitWaitBeyond(t *testing.T, waits *atomic.Int64, seen int64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for waits.Load() <= seen {
		if time.Now().After(deadline) {
			t.Fatal("the stream never started to wait")
		}
		time.Sleep(time.Millisecond)
	}
}

func AwaitWokenText(t *testing.T, texts <-chan string, want string, since time.Time) {
	t.Helper()
	deadline := time.After(wakeBound)
	for {
		select {
		case text := <-texts:
			if strings.Contains(text, want) {
				t.Logf("%q reached the stream after %s", want, time.Since(since).Round(time.Millisecond))
				return
			}
		case <-deadline:
			t.Fatalf("%q did not reach the stream within %s", want, wakeBound)
		}
	}
}

// SlowPoll raises the stream timer far above the wake bound the tests assert,
// so an event inside the bound can only have come from a file wake.
func SlowPoll(t *testing.T) {
	t.Helper()
	original := transcript.PollInterval
	transcript.PollInterval = 10 * time.Second
	t.Cleanup(func() { transcript.PollInterval = original })
}

// StreamWaits counts the times a stream has read its transcript and started
// to wait. A write after the count moves past a value reaches the stream only
// through a wake or the slow timer.
func StreamWaits(t *testing.T) *atomic.Int64 {
	t.Helper()
	waits := &atomic.Int64{}
	transcript.Waiting = func() { waits.Add(1) }
	t.Cleanup(func() { transcript.Waiting = nil })
	return waits
}
