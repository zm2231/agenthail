package surfaces

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

const (
	closedTurnRecords = `{"type":"user","uuid":"u0","message":{"content":"earlier"}}
{"type":"assistant","uuid":"a0","message":{"id":"m0","stop_reason":"end_turn","content":[{"type":"text","text":"earlier answer"}]}}
{"type":"system","subtype":"turn_duration"}
`
	promptRecord    = `{"type":"user","uuid":"u1","timestamp":"2026-10-18T22:07:50Z","message":{"content":"say one"}}` + "\n"
	thinkingRecord  = `{"type":"assistant","uuid":"a1-thinking","timestamp":"2026-10-18T22:07:54.199Z","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"thinking","thinking":"","signature":"sig"}]}}` + "\n"
	textRecord      = `{"type":"assistant","uuid":"a1-text","timestamp":"2026-10-18T22:07:54.2Z","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"ONE"}]}}` + "\n"
	hookRecord      = `{"type":"system","subtype":"stop_hook_summary","timestamp":"2026-10-18T22:07:54.3Z"}` + "\n"
	durationRecord  = `{"type":"system","subtype":"turn_duration","timestamp":"2026-10-18T22:07:54.4Z"}` + "\n"
	attachRecord    = `{"type":"attachment","timestamp":"2026-10-18T22:07:54.3Z"}` + "\n"
	interruptRecord = `{"type":"user","uuid":"interrupt","timestamp":"2026-10-18T22:07:54.3Z","message":{"content":"[Request interrupted by user]"}}` + "\n"
	nextRecord      = `{"type":"user","uuid":"u2","timestamp":"2026-10-18T22:08:00Z","message":{"content":"next"}}` + "\n"
)

func baselineCompletions(t *testing.T) (string, *surface.Session, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	testkit.AppendFile(t, path, closedTurnRecords)
	session := &surface.Session{ID: "source", Transcript: path}
	baseline, err := NewClaude(nil, t.TempDir()).ReadCompletions(context.Background(), session, "")
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
		{"turn_duration alone", []string{promptRecord, thinkingRecord, textRecord, durationRecord}, textAt},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, session, cursor := baselineCompletions(t)
			testkit.AppendFile(t, path, test.records...)
			batch, err := NewClaude(nil, t.TempDir()).ReadCompletions(context.Background(), session, cursor)
			if err != nil || len(batch.Events) != 1 {
				t.Fatalf("events=%+v err=%v", batch.Events, err)
			}
			event := batch.Events[0]
			if event.ID != "m1" || event.Text != "ONE" || event.InputID != "u1" || event.User != "say one" || !event.CompletedAt.Equal(test.at) {
				t.Fatalf("event=%+v", event)
			}
			if replay, err := NewClaude(nil, t.TempDir()).ReadCompletions(context.Background(), session, batch.Cursor); err != nil || len(replay.Events) != 0 {
				t.Fatalf("replay=%+v err=%v", replay, err)
			}
		})
	}
}

func TestClaudeCompletionSplitAcrossReadsEmitsFullTextOnceAfterRestart(t *testing.T) {
	path, session, cursor := baselineCompletions(t)
	testkit.AppendFile(t, path, promptRecord, thinkingRecord)
	pending, err := NewClaude(nil, t.TempDir()).ReadCompletions(context.Background(), session, cursor)
	if err != nil || len(pending.Events) != 0 {
		t.Fatalf("closing turn emitted early: %+v err=%v", pending.Events, err)
	}
	testkit.AppendFile(t, path, textRecord)
	stillPending, err := NewClaude(nil, t.TempDir()).ReadCompletions(context.Background(), session, pending.Cursor)
	if err != nil || len(stillPending.Events) != 0 {
		t.Fatalf("turn emitted before its close record: %+v err=%v", stillPending.Events, err)
	}
	testkit.AppendFile(t, path, durationRecord)
	batch, err := NewClaude(nil, t.TempDir()).ReadCompletions(context.Background(), session, stillPending.Cursor)
	if err != nil || len(batch.Events) != 1 || batch.Events[0].ID != "m1" || batch.Events[0].Text != "ONE" || batch.Events[0].InputID != "u1" {
		t.Fatalf("events=%+v err=%v", batch.Events, err)
	}
	if replay, err := NewClaude(nil, t.TempDir()).ReadCompletions(context.Background(), session, batch.Cursor); err != nil || len(replay.Events) != 0 {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
}

func TestClaudeObservationKeepsClosingTurnBusyUntilItCloses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	testkit.AppendFile(t, path, promptRecord, thinkingRecord, textRecord)
	claude := NewClaude(nil, t.TempDir())
	session := &surface.Session{ID: "source", Surface: surface.KindClaude, Transcript: path, Status: surface.StatusIdle}
	observation, err := claude.observe(context.Background(), session)
	if err != nil || observation.Status != surface.StatusBusy || observation.ActiveTurnID != "u1" || observation.CompletedTurnID != "" {
		t.Fatalf("closing turn observation=%+v err=%v", observation, err)
	}
	testkit.AppendFile(t, path, hookRecord)
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
	testkit.AppendFile(t, path, hookRecord)
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
	err := NewClaude(nil, t.TempDir()).Stream(context.Background(), &surface.Session{ID: "bridge", Surface: surface.KindClaude, Transcript: path}, "u1", func(event surface.StreamEvent) {
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
	testkit.AppendFile(t, path, promptRecord, thinkingRecord, textRecord)
	claude := NewClaude(nil, t.TempDir())
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
	testkit.AppendFile(t, path, durationRecord)
	if done := window(); len(done) != 1 || done["u1"] != 1 || session.ClaudeClosingTurn.TurnID != "" {
		t.Fatalf("closing turn not finished once: done=%v closing=%+v", done, session.ClaudeClosingTurn)
	}
}

func TestClaudeInterruptAfterClosingBlockInterruptsTheTurn(t *testing.T) {
	path, session, cursor := baselineCompletions(t)
	testkit.AppendFile(t, path, promptRecord, thinkingRecord, textRecord, interruptRecord, durationRecord)
	batch, err := NewClaude(nil, t.TempDir()).ReadCompletions(context.Background(), session, cursor)
	if err != nil || len(batch.Events) != 0 {
		t.Fatalf("interrupted turn relayed: %+v err=%v", batch.Events, err)
	}
	observed := writeTranscript(t, promptRecord+thinkingRecord+textRecord+interruptRecord)
	observation, err := NewClaude(nil, t.TempDir()).observe(context.Background(), &surface.Session{ID: "source", Surface: surface.KindClaude, Transcript: observed, Status: surface.StatusIdle})
	if err != nil || observation.CompletedTurnID != "" || observation.Status != surface.StatusIdle {
		t.Fatalf("interrupted turn completed: %+v err=%v", observation, err)
	}
	targeted := writeTranscript(t, promptRecord+thinkingRecord+textRecord+interruptRecord)
	err = NewClaude(nil, t.TempDir()).Stream(context.Background(), &surface.Session{ID: "bridge", Surface: surface.KindClaude, Transcript: targeted}, "u1", func(surface.StreamEvent) {}, time.Second)
	if err == nil || errors.Is(err, surface.ErrStreamWindow) {
		t.Fatalf("targeted stream err=%v", err)
	}
	source := writeTranscript(t, closedTurnRecords)
	sourceSession := seededClaudeStreamSession(t, source, "")
	testkit.AppendFile(t, source, promptRecord, thinkingRecord, textRecord, interruptRecord)
	var terminals []string
	err = NewClaude(nil, t.TempDir()).Stream(context.Background(), sourceSession, "", func(event surface.StreamEvent) {
		if event.Kind == "done" {
			terminals = append(terminals, event.TurnID+":"+event.Status)
		}
	}, 150*time.Millisecond)
	if !errors.Is(err, surface.ErrStreamWindow) || len(terminals) != 1 || terminals[0] != "u1:cancelled" {
		t.Fatalf("source stream err=%v terminals=%v", err, terminals)
	}
}

func TestClaudeSourceStreamDropsClosingTurnWhenTranscriptIsRewritten(t *testing.T) {
	path := writeTranscript(t, closedTurnRecords)
	session := seededClaudeStreamSession(t, path, "")
	testkit.AppendFile(t, path, promptRecord, thinkingRecord, textRecord)
	claude := NewClaude(nil, t.TempDir())
	if err := claude.Stream(context.Background(), session, "", func(surface.StreamEvent) {}, 150*time.Millisecond); !errors.Is(err, surface.ErrStreamWindow) || session.ClaudeClosingTurn.TurnID != "u1" {
		t.Fatalf("stream err=%v closing=%+v", err, session.ClaudeClosingTurn)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	var rewritten strings.Builder
	for rewritten.Len() <= int(info.Size()) {
		rewritten.WriteString(`{"type":"user","uuid":"v1","message":{"content":"new generation"}}` + "\n")
	}
	rewritten.WriteString(`{"type":"user","uuid":"v2","message":{"content":"after the old offset"}}` + "\n")
	if err := os.WriteFile(path, []byte(rewritten.String()), 0600); err != nil {
		t.Fatal(err)
	}
	done := map[string]int{}
	err = claude.Stream(context.Background(), session, "", func(event surface.StreamEvent) {
		if event.Kind == "done" {
			done[event.TurnID]++
		}
	}, 150*time.Millisecond)
	if !errors.Is(err, surface.ErrStreamWindow) || len(done) != 0 {
		t.Fatalf("stale closing turn finished after rewrite: err=%v done=%v", err, done)
	}
}

func TestClaudeSourceStreamDropsClosingTurnWhenTranscriptIsRewrittenMidWindow(t *testing.T) {
	path := writeTranscript(t, closedTurnRecords)
	session := seededClaudeStreamSession(t, path, "")
	testkit.AppendFile(t, path, promptRecord, thinkingRecord, textRecord)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	var rewritten strings.Builder
	for rewritten.Len() <= int(info.Size()) {
		rewritten.WriteString(`{"type":"user","uuid":"v1","message":{"content":"new generation"}}` + "\n")
	}
	rewritten.WriteString(`{"type":"user","uuid":"v2","message":{"content":"after the old offset"}}` + "\n")
	original := transcriptPollInterval
	transcriptPollInterval = 300 * time.Millisecond
	t.Cleanup(func() { transcriptPollInterval = original })
	var mu sync.Mutex
	done := map[string]int{}
	closingSeen := make(chan struct{})
	written := make(chan error, 1)
	go func() {
		<-closingSeen
		time.Sleep(100 * time.Millisecond)
		written <- os.WriteFile(path, []byte(rewritten.String()), 0600)
	}()
	var once sync.Once
	err = NewClaude(nil, t.TempDir()).Stream(context.Background(), session, "", func(event surface.StreamEvent) {
		mu.Lock()
		defer mu.Unlock()
		if event.Text == "ONE" {
			once.Do(func() { close(closingSeen) })
		}
		if event.Kind == "done" {
			done[event.TurnID]++
		}
	}, 700*time.Millisecond)
	if writeErr := <-written; writeErr != nil {
		t.Fatal(writeErr)
	}
	mu.Lock()
	defer mu.Unlock()
	if !errors.Is(err, surface.ErrStreamWindow) || len(done) != 0 {
		t.Fatalf("stale closing turn finished after mid-window rewrite: err=%v done=%v", err, done)
	}
}

// A session run through the SDK writes no turn_duration, so its final turn
// closes when the process records idle after the turn's last block.
func TestClaudeSDKTurnClosesWhenItsProcessRecordsIdle(t *testing.T) {
	textAt := time.Date(2026, 10, 18, 22, 7, 54, 200_000_000, time.UTC)
	home := t.TempDir()
	recordIdle := func(at time.Time) {
		t.Helper()
		dir := filepath.Join(home, ".claude", "sessions")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		record := fmt.Sprintf(`{"pid":%d,"sessionId":"bridge","status":"idle","statusUpdatedAt":%d}`, os.Getpid(), at.UnixMilli())
		if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(os.Getpid())+".json"), []byte(record), 0600); err != nil {
			t.Fatal(err)
		}
	}
	claude := NewClaude(nil, home)
	path := writeTranscript(t, closedTurnRecords)
	source := seededClaudeStreamSession(t, path, "uds")
	source.PID = os.Getpid()
	baseline, err := claude.ReadCompletions(context.Background(), source, "")
	if err != nil {
		t.Fatal(err)
	}
	testkit.AppendFile(t, path, promptRecord, thinkingRecord, textRecord)
	stale := *source
	stale.PID = 0

	recordIdle(textAt.Add(-time.Millisecond))
	for _, session := range []*surface.Session{source, &stale} {
		observed := *session
		observed.Status = surface.StatusIdle
		if observation, err := NewClaude(nil, home).observe(context.Background(), &observed); err != nil || observation.Status != surface.StatusBusy || observation.CompletedTurnID != "m0" {
			t.Fatalf("turn closed by an idle recorded before it: %+v err=%v", observation, err)
		}
	}
	if pending, err := claude.ReadCompletions(context.Background(), source, baseline.Cursor); err != nil || len(pending.Events) != 0 {
		t.Fatalf("completion before idle: %+v err=%v", pending.Events, err)
	}

	recordIdle(textAt.Add(24 * time.Millisecond))
	for _, session := range []*surface.Session{source, &stale} {
		observed := *session
		observed.Status = surface.StatusIdle
		observation, err := NewClaude(nil, home).observe(context.Background(), &observed)
		if err != nil || observation.Status != surface.StatusIdle || observation.CompletedTurnID != "m1" || observation.Reply == nil || observation.Reply.Text != "ONE" {
			t.Fatalf("idle turn observation=%+v err=%v", observation, err)
		}
	}
	batch, err := claude.ReadCompletions(context.Background(), source, baseline.Cursor)
	if err != nil || len(batch.Events) != 1 || batch.Events[0].ID != "m1" || batch.Events[0].Text != "ONE" || !batch.Events[0].CompletedAt.Equal(textAt) {
		t.Fatalf("events=%+v err=%v", batch.Events, err)
	}
	testkit.AppendFile(t, path, nextRecord)
	if replay, err := claude.ReadCompletions(context.Background(), source, batch.Cursor); err != nil || len(replay.Events) != 0 {
		t.Fatalf("settled turn completed again: %+v err=%v", replay, err)
	}
	read, err := claude.ReadSession(context.Background(), &stale, surface.SessionReadRequest{Limit: 20})
	if err != nil || read.Reply == nil || read.Reply.Text != "ONE" || !read.Reply.Done {
		t.Fatalf("reply=%+v err=%v", read.Reply, err)
	}
	done := map[string]int{}
	err = claude.Stream(context.Background(), source, "", func(event surface.StreamEvent) {
		if event.Kind == "done" {
			done[event.TurnID]++
		}
	}, 150*time.Millisecond)
	if !errors.Is(err, surface.ErrStreamWindow) || done["u1"] != 1 || source.ClaudeClosingTurn.TurnID != "" {
		t.Fatalf("stream err=%v done=%v closing=%+v", err, done, source.ClaudeClosingTurn)
	}
}

func TestAClosingTurnClearsWhenItsTranscriptIsRewrittenAtTheSameSizeAndTime(t *testing.T) {
	path := writeTranscript(t, closedTurnRecords)
	session := seededClaudeStreamSession(t, path, "")
	testkit.AppendFile(t, path, promptRecord, thinkingRecord, textRecord)
	// The stream parks between passes, so the rewrite and the restored
	// modification time land before it looks at the transcript again.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	parked, resume := make(chan struct{}), make(chan struct{})
	transcriptWaiting = func() {
		select {
		case parked <- struct{}{}:
		case <-ctx.Done():
			return
		}
		select {
		case <-resume:
		case <-ctx.Done():
		}
	}
	t.Cleanup(func() { transcriptWaiting = nil })
	nextPass := func() {
		t.Helper()
		select {
		case <-parked:
		case <-time.After(5 * time.Second):
			t.Fatal("the stream made no pass")
		}
	}
	done := make(chan error, 1)
	go func() {
		done <- NewClaude(nil, t.TempDir()).Stream(ctx, session, "", func(surface.StreamEvent) {}, time.Minute)
	}()
	nextPass()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte("TWO"), int64(bytes.Index(data, []byte(`"text":"ONE"`))+len(`"text":"`))); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	resume <- struct{}{}
	nextPass()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("stream err=%v", err)
	}
	if session.ClaudeClosingTurn.TurnID != "" {
		t.Fatalf("a closing turn outlived the rewrite of the bytes it closed in: %+v", session.ClaudeClosingTurn)
	}
}
