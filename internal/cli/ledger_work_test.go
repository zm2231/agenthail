package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestLedgerWorkPostsEachAction(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(string, map[string]any) (int, any) {
		return http.StatusOK, ledger.WorkResult{Readback: "intent ledger: ok"}
	})
	for _, args := range [][]string{
		{"work", "add", "widget-C1", "add", "the", "index"},
		{"work", "claim", "widget-W1"},
		{"work", "block", "widget-W1", "--reason", "waiting on the schema"},
	} {
		if _, err := captureStdout(t, func() error { return app.cmdLedger(args) }); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	got := *calls
	if got[0]["action"] != "add" || got[0]["clauseId"] != "widget-C1" || got[0]["text"] != "add the index" || got[0]["path"] != "/api/v1/ledger/work" {
		t.Fatalf("add request %v", got[0])
	}
	if got[1]["action"] != "claim" || got[1]["itemId"] != "widget-W1" || got[1]["sessionId"] != "s1" {
		t.Fatalf("claim request %v", got[1])
	}
	if got[2]["reason"] != "waiting on the schema" {
		t.Fatalf("block request %v", got[2])
	}
	if _, err := captureStdout(t, func() error { return app.cmdLedger([]string{"work", "add", "widget-C1"}) }); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("add without text: %v", err)
	}
}
