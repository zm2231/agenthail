package surfaces

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
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
			read, err := timelineClaude().IdleCompactionState(context.Background(), &surface.Session{Transcript: path}, surface.IdleCompactionRead{})
			if err != nil {
				t.Fatal(err)
			}
			if read.State.OneHourCache != tc.oneHour {
				t.Fatalf("state = %+v, want one-hour cache %v", read.State, tc.oneHour)
			}
		})
	}
}

func TestAnIdleReadContinuedFromItsOffsetMatchesReadingTheTranscriptAfresh(t *testing.T) {
	claude := timelineClaude()
	session := &surface.Session{Transcript: writeTranscript(t, claudeRequestRecord("2026-11-06T10:00:00Z", 500, 500, 0))}
	ctx := context.Background()
	read, err := claude.IdleCompactionState(ctx, session, surface.IdleCompactionRead{})
	if err != nil {
		t.Fatal(err)
	}
	for _, appended := range []string{
		claudeRequestRecord("2026-11-06T10:01:00Z", 0, 0, 900),
		`{"type":"system","subtype":"compact_boundary","timestamp":"2026-11-06T10:02:00Z"}` + "\n",
		claudeRequestRecord("2026-11-06T10:03:00Z", 100, 0, 300),
		`{"type":"assistant","timestamp":"2026-11-06T10:04:00Z","message":{"model":"claude-fixture","usage":{"input_tokens":`,
		`7,"cache_creation_input_tokens":40,"cache_creation":{"ephemeral_1h_input_tokens":40}}}}` + "\n",
	} {
		testkit.AppendFile(t, session.Transcript, appended)
		if read, err = claude.IdleCompactionState(ctx, session, read); err != nil {
			t.Fatal(err)
		}
		fresh, err := claude.IdleCompactionState(ctx, session, surface.IdleCompactionRead{})
		if err != nil {
			t.Fatal(err)
		}
		if read != fresh {
			t.Fatalf("after appending %q: continued %+v, afresh %+v", appended, read, fresh)
		}
	}
	if !read.State.OneHourCache || read.State.ContextTokens != 47 || read.State.CompactedSince {
		t.Fatalf("final state = %+v", read.State)
	}

	replacement := filepath.Join(t.TempDir(), "replacement.jsonl")
	if err := os.WriteFile(replacement, []byte(strings.Repeat(claudeRequestRecord("2026-11-06T11:00:00Z", 0, 0, 900), 20)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, session.Transcript); err != nil {
		t.Fatal(err)
	}
	if read, err = claude.IdleCompactionState(ctx, session, read); err != nil {
		t.Fatal(err)
	}
	if read.State.OneHourCache || !read.State.LastRequestAt.Equal(time.Date(2026, 11, 6, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("after the transcript was replaced: %+v, want only the replacement's requests", read.State)
	}

	testkit.AppendFile(t, session.Transcript, strings.Repeat(`{"type":"user","message":{"content":"`+strings.Repeat("x", 1<<20)+`"}}`+"\n", 5), claudeRequestRecord("2026-11-06T12:00:00Z", 40, 40, 0))
	if read, err = claude.IdleCompactionState(ctx, session, read); err != nil {
		t.Fatal(err)
	}
	if !read.State.OneHourCache || !read.State.LastRequestAt.Equal(time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("after more than the tail was appended: %+v", read.State)
	}
}
