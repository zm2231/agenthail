package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestLedgerDeadlinesAcceptMinutesHoursAndDays(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	for value, want := range map[string]time.Duration{"30m": 30 * time.Minute, "4h": 4 * time.Hour, "2d": 48 * time.Hour} {
		got, err := parseLedgerDeadline(value, now)
		if err != nil || got.Sub(now) != want {
			t.Fatalf("%s = %v %v", value, got.Sub(now), err)
		}
	}
	for _, bad := range []string{"", "soon", "-1h", "0d", "xd"} {
		if _, err := parseLedgerDeadline(bad, now); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestLedgerQuestionPostsTheQuestionAndSaysWhatHappensIfUnanswered(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(_ string, body map[string]any) (int, any) {
		return http.StatusOK, ledger.Question{ID: "widget-Q1", Target: "widget-G1", Kind: ledger.QuestionScope, DefaultAnswer: "a smaller first version", Narrows: true, DeadlineMs: int64(body["deadlineMs"].(float64))}
	})
	before := time.Now()
	out, err := captureStdout(t, func() error {
		return app.cmdLedger([]string{"question", "--to", "widget-G1", "How", "broad?", "--recommendation", "everything", "--default", "a smaller first version", "--deadline", "4h", "--kind", "scope", "--narrows"})
	})
	if err != nil {
		t.Fatal(err)
	}
	got := (*calls)[0]
	deadline := time.UnixMilli(int64(got["deadlineMs"].(float64)))
	if got["path"] != "/api/v1/ledger/question" || got["text"] != "How broad?" || got["narrows"] != true || got["kind"] != "scope" || deadline.Sub(before) < 4*time.Hour-time.Minute {
		t.Fatalf("request %v", got)
	}
	if !strings.Contains(out, "+widget-Q1 on widget-G1") || !strings.Contains(out, `"a smaller first version" waits for the person's ack`) {
		t.Fatalf("output %q", out)
	}
	if _, err := captureStdout(t, func() error {
		return app.cmdLedger([]string{"question", "--to", "widget-G1", "How", "broad?", "--deadline", "4h"})
	}); err == nil || !strings.Contains(err.Error(), "--kind") {
		t.Fatalf("question without --kind: %v", err)
	}
}
