package transcript

import (
	"context"
	"time"

	"github.com/zm2231/agenthail/internal/filewake"
)

// PollInterval is the longest a live stream waits before re-reading
// its source; a reported write to the file wakes it sooner. Tests shorten it
// so stream windows do not dominate the suite.
var PollInterval = 300 * time.Millisecond

// Waiting, when set by a test, runs each time a stream has read its
// transcript and starts to wait.
var Waiting func()

// Await waits for a reported write to the transcript or the poll
// interval, whichever comes first.
func Await(ctx context.Context, wake *filewake.Wake) error {
	wake.Refresh()
	if Waiting != nil {
		Waiting()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-wake.C():
	case <-time.After(PollInterval):
	}
	return nil
}
