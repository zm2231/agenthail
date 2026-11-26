package cli

import (
	"net/http"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestAHolderReleasesOrHandsOffAnItemFromTheCLI(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(string, map[string]any) (int, any) {
		return http.StatusOK, ledger.WorkResult{Readback: "intent ledger: ok"}
	})
	for _, args := range [][]string{
		{"release", "widget-W1", "--reason", "moving to the parser first"},
		{"handoff", "widget-W1", "--done", "found the double read", "--decisions", "read it once"},
	} {
		if _, err := captureStdout(t, func() error { return app.cmdLedger(args) }); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	got := *calls
	if len(got) != 2 || got[0]["action"] != "release" || got[0]["itemId"] != "widget-W1" || got[0]["reason"] != "moving to the parser first" {
		t.Fatalf("release request %v", got)
	}
	handoff, _ := got[1]["handoff"].(map[string]any)
	if got[1]["action"] != "handoff" || got[1]["done"] != nil || handoff["done"].([]any)[0] != "found the double read" || handoff["decisions"].([]any)[0] != "read it once" {
		t.Fatalf("handoff request %v", got[1])
	}
}
