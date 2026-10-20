package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

const claudeUsageLimitLine = `{"type":"assistant","uuid":"u2","timestamp":"2026-10-09T10:11:12.653Z","message":{"id":"m2","model":"<synthetic>","role":"assistant","stop_reason":"stop_sequence","type":"message","content":[{"type":"text","text":"You've hit your session limit · resets 7am (America/New_York)"}]},"apiError":"usage_limit_reached","apiErrorParams":{"rate_limit_info":{"status":"rejected","resetsAt":1791543600,"rateLimitType":"five_hour"}},"quotaLimits":{"status":"rejected","resetsAt":1791543600,"rateLimitType":"five_hour"},"error":"rate_limit","isApiErrorMessage":true,"apiErrorStatus":429}`

func TestAUsageLimitRecordIsANoticeWithItsResetTime(t *testing.T) {
	var record map[string]any
	if err := json.Unmarshal([]byte(claudeUsageLimitLine), &record); err != nil {
		t.Fatal(err)
	}
	items := claudeTimelineItems(record)
	if len(items) != 1 {
		t.Fatalf("items=%+v", items)
	}
	notice := items[0]
	if notice.Kind != "notice" || notice.Status != surface.TimelineUsageLimit || notice.Role != "" || !strings.Contains(notice.Text, "session limit") {
		t.Fatalf("notice=%+v", notice)
	}
	if notice.ResetsAt != time.Unix(1791543600, 0).UTC().Format(time.RFC3339) {
		t.Fatalf("resetsAt=%q", notice.ResetsAt)
	}
	delete(record, "error")
	delete(record, "apiError")
	if other := claudeTimelineItems(record); len(other) != 1 || other[0].Kind != "notice" || other[0].Status != "api_error" {
		t.Fatalf("another API error=%+v", other)
	}
}

func TestAUsageLimitedTurnEndsTheReplyWithTheLimit(t *testing.T) {
	records := []transcriptRecord{
		{line: []byte(`{"type":"user","uuid":"u1","timestamp":"2026-10-09T10:11:10Z","message":{"role":"user","content":"keep going"}}`)},
		{line: []byte(claudeUsageLimitLine)},
	}
	reply := claudeTranscriptReply(records, "claude", nil)
	if !reply.Done || !strings.Contains(reply.Error, "out of usage") || !strings.Contains(reply.Error, "session limit") || !strings.Contains(reply.Error, "thread move") {
		t.Fatalf("reply=%+v", reply)
	}
}

// usageLimitedConversation writes a conversation whose last turn the usage
// limit refused, as Claude Code records it.
func usageLimitedConversation(t *testing.T) (*Claude, *surface.Session) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	fixture := claudetest.New(t)
	transcript := filepath.Join(fixture.Root, "projects", "fixture-project", fixtureConversation+".jsonl")
	user := `{"type":"user","uuid":"u1","sessionId":"` + fixtureConversation + `","cwd":"` + fixture.Home + `","timestamp":"2026-10-09T10:11:10Z","message":{"role":"user","content":"keep going"}}`
	if err := os.MkdirAll(filepath.Dir(transcript), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte(user+"\n"+claudeUsageLimitLine+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return NewClaude(nil, fixture.Home), &surface.Session{ID: fixtureConversation, Surface: surface.KindClaude, Transcript: transcript}
}

func TestAUsageLimitedTurnReadsAsANoticeAndACompletedTurn(t *testing.T) {
	c, sess := usageLimitedConversation(t)
	read, err := c.ReadSession(context.Background(), sess, surface.SessionReadRequest{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	var notices, cancelled, done int
	for _, item := range read.Items {
		switch {
		case item.Kind == "notice" && item.Status == surface.TimelineUsageLimit && item.ResetsAt != "":
			notices++
		case item.Kind == "done" && item.Status == "cancelled":
			cancelled++
		case item.Kind == "done":
			done++
		case item.Kind == "message" && item.Role == "assistant":
			t.Fatalf("the limit was read as Claude's reply: %+v", item)
		}
	}
	if notices != 1 || cancelled != 0 || done != 1 {
		t.Fatalf("notices=%d cancelled=%d done=%d items=%+v", notices, cancelled, done, read.Items)
	}
	if read.Reply == nil || !strings.Contains(read.Reply.Error, "out of usage") {
		t.Fatalf("reply=%+v", read.Reply)
	}
}

func TestObservingAUsageLimitedTurnEndsItWithTheLimit(t *testing.T) {
	c, sess := usageLimitedConversation(t)
	observation, err := c.Observe(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Status == surface.StatusBusy || observation.Reply == nil || !observation.Reply.Done {
		t.Fatalf("observation=%+v reply=%+v", observation, observation.Reply)
	}
	if !strings.Contains(observation.Reply.Error, "out of usage") || !strings.Contains(observation.Reply.Error, "thread move") || strings.Contains(observation.Reply.Text, "session limit") {
		t.Fatalf("reply=%+v", observation.Reply)
	}
}

func TestTheSyntheticModelOfAnAPIErrorIsNotTheSessionModel(t *testing.T) {
	if model := claudeAssistantModel([]byte(claudeUsageLimitLine)); model != "" {
		t.Fatalf("model=%q", model)
	}
}

func TestStreamingAUsageLimitedTurnEndsItFailedWithTheLimit(t *testing.T) {
	c, sess := usageLimitedConversation(t)
	var events []surface.StreamEvent
	started := time.Now()
	err := c.Stream(context.Background(), sess, "u1", func(event surface.StreamEvent) { events = append(events, event) }, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatalf("the stream waited %s for a turn that had ended", time.Since(started))
	}
	var ended *surface.StreamEvent
	notices := 0
	for i, event := range events {
		if event.Kind == "done" && event.TurnID == "u1" {
			ended = &events[i]
		}
		if event.Kind == "notice" && event.Status == surface.TimelineUsageLimit && event.ResetsAt != "" {
			notices++
		}
	}
	if ended == nil || !ended.Failed() || !strings.Contains(ended.Text, "out of usage") || notices != 1 {
		t.Fatalf("ended=%+v notices=%d events=%+v", ended, notices, events)
	}
}
