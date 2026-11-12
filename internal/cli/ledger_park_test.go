package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestLedgerParkParksEachItemAndKeepsGoingPastARefusal(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(_ string, body map[string]any) (int, any) {
		if body["itemId"] == "widget-W2" {
			return http.StatusConflict, map[string]any{"error": map[string]string{"code": "refused", "message": "widget-W2 is done; park needs it open, in_progress or blocked"}}
		}
		return http.StatusOK, ledger.WorkResult{Readback: "intent ledger: " + body["itemId"].(string) + " parked"}
	})
	output, err := captureStdout(t, func() error {
		return app.cmdLedger([]string{"park", "widget-W1", "widget-W2", "widget-W3", "--later", "after the release", "--until", "2026-12-01"})
	})
	if err == nil || !strings.Contains(err.Error(), "widget-W2") {
		t.Fatalf("err=%v, want the refused item named", err)
	}
	if !strings.Contains(output, "widget-W1 parked") || !strings.Contains(output, "widget-W2 not parked") || !strings.Contains(output, "widget-W3 parked") {
		t.Fatalf("output=%q", output)
	}
	got := *calls
	if len(got) != 3 {
		t.Fatalf("%d requests, want one per item", len(got))
	}
	park, _ := got[2]["park"].(map[string]any)
	if got[2]["action"] != "park" || got[2]["itemId"] != "widget-W3" || park["kind"] != "later" || park["why"] != "after the release" || !strings.HasPrefix(park["revisit"].(string), "2026-12-0") {
		t.Fatalf("park request %v", got[2])
	}
}

func TestLedgerParkReadsEachKindFromItsFlags(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(string, map[string]any) (int, any) {
		return http.StatusOK, ledger.WorkResult{Readback: "intent ledger: ok"}
	})
	for _, args := range [][]string{
		{"work", "park", "widget-W1", "--usage", "claude:bb", "--for-session", "s9", "--remaining", "the second half"},
		{"work", "park", "widget-W1", "--person", "titles only, or bodies too?", "--ask", "ask-1"},
		{"park", "widget-W1", "--idea", "--utterance", "claude-prompt:1", "--goal", "search everywhere", "--first-test", "one query, two projects"},
		{"unpark", "widget-W1", "widget-W4"},
	} {
		if _, err := captureStdout(t, func() error { return app.cmdLedger(args) }); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	got := *calls
	usage := got[0]["park"].(map[string]any)
	target := usage["usage"].(map[string]any)
	handoff := got[0]["handoff"].(map[string]any)
	if usage["kind"] != "usage" || target["provider"] != "claude" || target["root"] != "bb" || got[0]["forSession"] != "s9" || handoff["remaining"].([]any)[0] != "the second half" {
		t.Fatalf("usage park %v", got[0])
	}
	person := got[1]["park"].(map[string]any)["person"].(map[string]any)
	if person["question"] != "titles only, or bodies too?" || person["askId"] != "ask-1" {
		t.Fatalf("person park %v", got[1])
	}
	idea := got[2]["park"].(map[string]any)["idea"].(map[string]any)
	if idea["utteranceId"] != "claude-prompt:1" || idea["goal"] != "search everywhere" || idea["firstTest"] != "one query, two projects" {
		t.Fatalf("idea park %v", got[2])
	}
	if got[3]["action"] != "unpark" || got[4]["action"] != "unpark" || got[4]["itemId"] != "widget-W4" || got[3]["park"] != nil {
		t.Fatalf("unpark requests %v %v", got[3], got[4])
	}
	for _, args := range [][]string{
		{"park", "widget-W1"},
		{"park", "widget-W1", "--usage", "codex", "--later", "both"},
		{"park", "--later", "no item"},
	} {
		if _, err := captureStdout(t, func() error { return app.cmdLedger(args) }); err == nil {
			t.Fatalf("%v was accepted", args)
		}
	}
}

func TestParseRevisitTakesADateATimeOrASpan(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	for value, want := range map[string]time.Time{
		"2026-11-14T08:00:00Z": time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC),
		"3d":                   now.Add(72 * time.Hour),
		"2w":                   now.Add(14 * 24 * time.Hour),
		"36h":                  now.Add(36 * time.Hour),
	} {
		if got, err := parseRevisit(value, now); err != nil || !got.Equal(want) {
			t.Fatalf("%s = %v, %v; want %v", value, got, err, want)
		}
	}
	if got, err := parseRevisit("2026-12-01", now); err != nil || got.In(time.Local).Format("2006-01-02") != "2026-12-01" {
		t.Fatalf("date = %v, %v", got, err)
	}
	for _, bad := range []string{"soon", "-2d", "0w"} {
		if _, err := parseRevisit(bad, now); err == nil {
			t.Fatalf("%q was accepted", bad)
		}
	}
}
