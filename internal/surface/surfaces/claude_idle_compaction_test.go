package surfaces

import (
	"context"
	"fmt"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func claudeRequestRecord(at string, write, oneHour, read int64) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"model":"claude-fixture","usage":{"input_tokens":10,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"cache_creation":{"ephemeral_1h_input_tokens":%d}}}}`+"\n", at, write, read, oneHour)
}

func TestARequestThatOnlyReadsTheCacheKeepsTheCacheTheLastWriteChose(t *testing.T) {
	for _, tc := range []struct {
		name    string
		records string
		oneHour bool
	}{
		{"read after a one-hour write", claudeRequestRecord("2026-11-06T10:00:00Z", 500, 500, 0) + claudeRequestRecord("2026-11-06T10:01:00Z", 0, 0, 900), true},
		{"read after a five-minute write", claudeRequestRecord("2026-11-06T10:00:00Z", 500, 0, 0) + claudeRequestRecord("2026-11-06T10:01:00Z", 0, 0, 900), false},
		{"five-minute write after a one-hour write", claudeRequestRecord("2026-11-06T10:00:00Z", 500, 500, 0) + claudeRequestRecord("2026-11-06T10:01:00Z", 100, 0, 900), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTranscript(t, tc.records)
			state, err := timelineClaude().IdleCompactionState(context.Background(), &surface.Session{Transcript: path})
			if err != nil {
				t.Fatal(err)
			}
			if state.OneHourCache != tc.oneHour {
				t.Fatalf("state = %+v, want one-hour cache %v", state, tc.oneHour)
			}
		})
	}
}
