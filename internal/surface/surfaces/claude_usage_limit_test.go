package surfaces

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
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
