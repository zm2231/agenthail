package cli

import (
	"net/http"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestLedgerOwnProgressAndWaitPostClauseActions(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(string, map[string]any) (int, any) {
		return http.StatusOK, ledger.ClauseResult{Readback: "intent ledger: ok"}
	})
	before := time.Now()
	for _, args := range [][]string{
		{"own", "widget-C1"},
		{"progress", "widget-C1", "--ref", "commit 1a2b3c"},
		{"wait", "widget-C1", "--condition", "CI on #12", "--deadline", "30m"},
	} {
		if _, err := captureStdout(t, func() error { return app.cmdLedger(args) }); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	got := *calls
	if got[0]["path"] != "/api/v1/ledger/clause" || got[0]["action"] != "own" || got[0]["clauseId"] != "widget-C1" || got[0]["sessionId"] != "s1" {
		t.Fatalf("own %v", got[0])
	}
	if got[1]["action"] != "progress" || got[1]["ref"] != "commit 1a2b3c" {
		t.Fatalf("progress %v", got[1])
	}
	deadline := time.UnixMilli(int64(got[2]["deadlineMs"].(float64)))
	if got[2]["action"] != "wait" || got[2]["condition"] != "CI on #12" || deadline.Sub(before) < 29*time.Minute {
		t.Fatalf("wait %v", got[2])
	}
	if _, err := captureStdout(t, func() error { return app.cmdLedger([]string{"wait", "widget-C1", "--condition", "x"}) }); err == nil {
		t.Fatal("wait without a deadline accepted")
	}
}
