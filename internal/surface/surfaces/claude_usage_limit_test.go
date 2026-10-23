package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
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
	reply, _ := claudeTranscriptReply(records, "claude", nil)
	if !reply.Done || !strings.Contains(reply.Error, "out of usage") || !strings.Contains(reply.Error, "session limit") {
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

func TestAUsageLimitedTurnReadsAsANoticeAndAFailedTurn(t *testing.T) {
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
			if !strings.Contains(item.Text, "out of usage") || item.Turn == nil || item.Turn.State != surface.TurnFailed || item.Turn.Model == claudeSyntheticModel {
				t.Fatalf("the refused turn's done item=%+v turn=%+v", item, item.Turn)
			}
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
	if !strings.Contains(observation.Reply.Error, "out of usage") || strings.Contains(observation.Reply.Text, "session limit") {
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
	if ended.Turn == nil || ended.Turn.State != surface.TurnFailed || ended.Turn.Model == claudeSyntheticModel {
		t.Fatalf("the refused turn's metadata=%+v", ended.Turn)
	}
}

// A session whose last turn a usage limit refused is limited until the limit
// resets or a later turn starts, and that limit outranks the permission
// prompt its process record still reports.
func TestAUsageLimitedSessionIsLimitedUntilItsResetOrItsNextTurn(t *testing.T) {
	future := time.Now().Add(time.Hour).Truncate(time.Second).UTC()
	past := time.Now().Add(-time.Minute).Truncate(time.Second).UTC()
	user := `{"type":"user","uuid":"u1","timestamp":"2026-10-09T10:11:10Z","message":{"role":"user","content":"keep going"}}`
	limited := func(resetsAt time.Time) string {
		return strings.ReplaceAll(claudeUsageLimitLine, "1791543600", strconv.FormatInt(resetsAt.Unix(), 10))
	}
	unknown := strings.NewReplacer(`,"resetsAt":1791543600`, "").Replace(claudeUsageLimitLine)
	for _, test := range []struct {
		name  string
		lines []string
		want  *surface.UsageLimit
	}{
		{"until its reset", []string{user, limited(future)}, &surface.UsageLimit{ResetsAt: future}},
		{"with no reset time given", []string{user, unknown}, &surface.UsageLimit{}},
		{"after its reset", []string{user, limited(past)}, nil},
		{"once the next turn starts", []string{user, limited(future), strings.ReplaceAll(user, `"u1"`, `"u3"`)}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			writeClaudeRecord(t, home, map[string]any{"kind": "interactive", "entrypoint": "cli", "status": "waiting", "waitingFor": "permission prompt", "statusUpdatedAt": time.Now().UnixMilli()})
			claude := NewClaude(nil, home)
			session := terminalSession(t)
			session.Transcript = writeTranscript(t, strings.Join(test.lines, "\n"))
			observation, err := claude.Observe(context.Background(), session)
			if err != nil {
				t.Fatal(err)
			}
			if !surface.SameUsageLimit(observation.UsageLimit, test.want) {
				t.Fatalf("usage limit = %+v, want %+v", observation.UsageLimit, test.want)
			}
			if test.want != nil && observation.WaitingOn != nil {
				t.Fatalf("a limited session also waits on %+v", observation.WaitingOn)
			}
		})
	}
}

func TestObservingAUsageLimitedTurnReportsTheStopAfterTheLimitResets(t *testing.T) {
	c, sess := usageLimitedConversation(t)
	observation, err := c.Observe(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	stop := observation.LimitStop
	if stop == nil || stop.TurnID != "u1" || observation.UsageAccount != claudeTranscriptRoot(sess.Transcript) || !stop.ResetsAt.Equal(time.Unix(1791543600, 0)) || !stop.RefusedAt.Equal(time.Date(2026, 10, 9, 10, 11, 12, 653000000, time.UTC)) {
		t.Fatalf("stop=%+v", stop)
	}
	if observation.UsageLimit != nil {
		t.Fatalf("a limit that reset still reads as current: %+v", observation.UsageLimit)
	}
}

func TestATurnThatSucceedsAfterTheLimitEndsTheStop(t *testing.T) {
	c, sess := usageLimitedConversation(t)
	later := `{"type":"user","uuid":"u3","sessionId":"` + fixtureConversation + `","timestamp":"2026-10-09T12:00:00Z","message":{"role":"user","content":"continue"}}
{"type":"assistant","uuid":"a3","timestamp":"2026-10-09T12:00:05Z","message":{"id":"m3","model":"claude-opus-5-5","role":"assistant","stop_reason":"end_turn","type":"message","content":[{"type":"text","text":"done"}]}}
{"type":"system","subtype":"turn_duration","timestamp":"2026-10-09T12:00:06Z"}
`
	file, err := os.OpenFile(sess.Transcript, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(later); err != nil {
		t.Fatal(err)
	}
	file.Close()
	observation, err := c.Observe(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	if observation.LimitStop != nil || !observation.SettledAt.Equal(time.Date(2026, 10, 9, 12, 0, 5, 0, time.UTC)) {
		t.Fatalf("stop=%+v settled=%s", observation.LimitStop, observation.SettledAt)
	}
}
