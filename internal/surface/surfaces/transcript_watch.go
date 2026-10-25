package surfaces

import (
	"context"
	"time"

	"github.com/zm2231/agenthail/internal/filewake"
)

// transcriptPollInterval is the longest a live stream waits before re-reading
// its source; a reported write to the file wakes it sooner. Tests shorten it
// so stream windows do not dominate the suite.
var transcriptPollInterval = 300 * time.Millisecond

// transcriptWaiting, when set by a test, runs each time a stream has read its
// transcript and starts to wait.
var transcriptWaiting func()

// awaitTranscript waits for a reported write to the transcript or the poll
// interval, whichever comes first.
func awaitTranscript(ctx context.Context, wake *filewake.Wake) error {
	wake.Refresh()
	if transcriptWaiting != nil {
		transcriptWaiting()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-wake.C():
	case <-time.After(transcriptPollInterval):
	}
	return nil
}
