package surfaces

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	closedTurnRecords = `{"type":"user","uuid":"u0","message":{"content":"earlier"}}
{"type":"assistant","uuid":"a0","message":{"id":"m0","stop_reason":"end_turn","content":[{"type":"text","text":"earlier answer"}]}}
{"type":"system","subtype":"turn_duration"}
`
	promptRecord   = `{"type":"user","uuid":"u1","timestamp":"2026-10-18T22:07:50Z","message":{"content":"say one"}}` + "\n"
	thinkingRecord = `{"type":"assistant","uuid":"a1-thinking","timestamp":"2026-10-18T22:07:54.199Z","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"thinking","thinking":"","signature":"sig"}]}}` + "\n"
	textRecord     = `{"type":"assistant","uuid":"a1-text","timestamp":"2026-10-18T22:07:54.2Z","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"ONE"}]}}` + "\n"
	hookRecord     = `{"type":"system","subtype":"stop_hook_summary","timestamp":"2026-10-18T22:07:54.3Z"}` + "\n"
	durationRecord = `{"type":"system","subtype":"turn_duration","timestamp":"2026-10-18T22:07:54.4Z"}` + "\n"
	attachRecord   = `{"type":"attachment","timestamp":"2026-10-18T22:07:54.3Z"}` + "\n"
	nextRecord     = `{"type":"user","uuid":"u2","timestamp":"2026-10-18T22:08:00Z","message":{"content":"next"}}` + "\n"
)

func appendRecords(t *testing.T, path string, records ...string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(strings.Join(records, "")); err != nil {
		t.Fatal(err)
	}
}

func baselineCompletions(t *testing.T) (string, *surface.Session, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	appendRecords(t, path, closedTurnRecords)
	session := &surface.Session{ID: "source", Transcript: path}
	baseline, err := NewClaude("", t.TempDir()).ReadCompletions(context.Background(), session, "")
	if err != nil || len(baseline.Events) != 0 {
		t.Fatalf("baseline=%+v err=%v", baseline, err)
	}
	return path, session, baseline.Cursor
}

func TestClaudeCompletionClosesAfterLastContentBlock(t *testing.T) {
	textAt := time.Date(2026, 10, 18, 22, 7, 54, 200_000_000, time.UTC)
	for _, test := range []struct {
		name    string
		records []string
		at      time.Time
	}{
		{"thinking then text", []string{promptRecord, thinkingRecord, textRecord, hookRecord, durationRecord}, textAt},
		{"no turn_duration closes on next user", []string{promptRecord, thinkingRecord, textRecord, nextRecord}, textAt},
		{"text only", []string{promptRecord, textRecord, attachRecord}, textAt},
		{"turn_duration alone", []string{promptRecord, thinkingRecord, textRecord, durationRecord}, time.Date(2026, 10, 18, 22, 7, 54, 400_000_000, time.UTC)},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, session, cursor := baselineCompletions(t)
			appendRecords(t, path, test.records...)
			batch, err := NewClaude("", t.TempDir()).ReadCompletions(context.Background(), session, cursor)
			if err != nil || len(batch.Events) != 1 {
				t.Fatalf("events=%+v err=%v", batch.Events, err)
			}
			event := batch.Events[0]
			if event.ID != "m1" || event.Text != "ONE" || event.InputID != "u1" || event.User != "say one" || !event.CompletedAt.Equal(test.at) {
				t.Fatalf("event=%+v", event)
			}
			if replay, err := NewClaude("", t.TempDir()).ReadCompletions(context.Background(), session, batch.Cursor); err != nil || len(replay.Events) != 0 {
				t.Fatalf("replay=%+v err=%v", replay, err)
			}
		})
	}
}

func TestClaudeCompletionSplitAcrossReadsEmitsFullTextOnceAfterRestart(t *testing.T) {
	path, session, cursor := baselineCompletions(t)
	appendRecords(t, path, promptRecord, thinkingRecord)
	pending, err := NewClaude("", t.TempDir()).ReadCompletions(context.Background(), session, cursor)
	if err != nil || len(pending.Events) != 0 {
		t.Fatalf("closing turn emitted early: %+v err=%v", pending.Events, err)
	}
	appendRecords(t, path, textRecord)
	stillPending, err := NewClaude("", t.TempDir()).ReadCompletions(context.Background(), session, pending.Cursor)
	if err != nil || len(stillPending.Events) != 0 {
		t.Fatalf("turn emitted before its close record: %+v err=%v", stillPending.Events, err)
	}
	appendRecords(t, path, durationRecord)
	batch, err := NewClaude("", t.TempDir()).ReadCompletions(context.Background(), session, stillPending.Cursor)
	if err != nil || len(batch.Events) != 1 || batch.Events[0].ID != "m1" || batch.Events[0].Text != "ONE" || batch.Events[0].InputID != "u1" {
		t.Fatalf("events=%+v err=%v", batch.Events, err)
	}
	if replay, err := NewClaude("", t.TempDir()).ReadCompletions(context.Background(), session, batch.Cursor); err != nil || len(replay.Events) != 0 {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
}

func TestClaudeObservationKeepsClosingTurnBusyUntilItCloses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	appendRecords(t, path, promptRecord, thinkingRecord, textRecord)
	claude := NewClaude("", t.TempDir())
	session := &surface.Session{ID: "source", Surface: surface.KindClaude, Transcript: path, Status: surface.StatusIdle}
	observation, err := claude.observe(context.Background(), session)
	if err != nil || observation.Status != surface.StatusBusy || observation.ActiveTurnID != "u1" || observation.CompletedTurnID != "" {
		t.Fatalf("closing turn observation=%+v err=%v", observation, err)
	}
	appendRecords(t, path, hookRecord)
	observation, err = claude.observe(context.Background(), session)
	if err != nil || observation.Status != surface.StatusIdle || observation.CompletedTurnID != "m1" || observation.Reply == nil || observation.Reply.Text != "ONE" || !observation.Reply.Done {
		t.Fatalf("closed turn observation=%+v err=%v", observation, err)
	}
}

func TestClaudeTimelineReplyWaitsForLastContentBlock(t *testing.T) {
	path := writeTranscript(t, closedTurnRecords+promptRecord+thinkingRecord+textRecord)
	read, err := readTimeline(context.Background(), path, "claude", 0, 20)
	if err != nil || read.Reply == nil || read.Reply.Text != "ONE" || read.Reply.Done {
		t.Fatalf("closing reply=%+v err=%v", read.Reply, err)
	}
	appendRecords(t, path, hookRecord)
	read, err = readTimeline(context.Background(), path, "claude", 0, 20)
	if err != nil || read.Reply == nil || read.Reply.Text != "ONE" || !read.Reply.Done {
		t.Fatalf("closed reply=%+v err=%v", read.Reply, err)
	}
}

func TestClaudeTargetedStreamWaitsForLastContentBlock(t *testing.T) {
	path := writeTranscript(t, promptRecord+thinkingRecord)
	go func() {
		time.Sleep(60 * time.Millisecond)
		file, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		defer file.Close()
		file.WriteString(textRecord + durationRecord)
	}()
	var mu sync.Mutex
	var kinds []string
	err := NewClaude("", t.TempDir()).Stream(context.Background(), &surface.Session{ID: "bridge", Surface: surface.KindClaude, Transcript: path}, "u1", func(event surface.StreamEvent) {
		mu.Lock()
		defer mu.Unlock()
		if event.Kind == "done" || event.Text == "ONE" {
			kinds = append(kinds, event.Kind+":"+event.Text)
		}
	}, 2*time.Second)
	mu.Lock()
	defer mu.Unlock()
	if err != nil || len(kinds) != 2 || kinds[1] != "done:" || !strings.HasSuffix(kinds[0], ":ONE") {
		t.Fatalf("stream err=%v events=%v", err, kinds)
	}
}

func TestClaudeSourceStreamCarriesClosingTurnAcrossWindows(t *testing.T) {
	path := writeTranscript(t, closedTurnRecords)
	session := seededClaudeStreamSession(t, path, "")
	appendRecords(t, path, promptRecord, thinkingRecord, textRecord)
	claude := NewClaude("", t.TempDir())
	window := func() map[string]int {
		done := map[string]int{}
		err := claude.Stream(context.Background(), session, "", func(event surface.StreamEvent) {
			if event.Kind == "done" {
				done[event.TurnID]++
			}
		}, 150*time.Millisecond)
		if !errors.Is(err, surface.ErrStreamWindow) {
			t.Fatalf("stream err=%v", err)
		}
		return done
	}
	if done := window(); len(done) != 0 || session.ClaudeClosingTurn.TurnID != "u1" || session.ClaudeClosingTurn.MessageID != "m1" {
		t.Fatalf("closing turn finished early: done=%v closing=%+v", done, session.ClaudeClosingTurn)
	}
	appendRecords(t, path, durationRecord)
	if done := window(); len(done) != 1 || done["u1"] != 1 || session.ClaudeClosingTurn.TurnID != "" {
		t.Fatalf("closing turn not finished once: done=%v closing=%+v", done, session.ClaudeClosingTurn)
	}
}
