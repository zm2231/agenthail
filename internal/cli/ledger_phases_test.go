package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestLedgerVerifyMarksAnItemAndPhasesNamesThePhases(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(path string, body map[string]any) (int, any) {
		if path == "/api/v1/ledger/phases" {
			return http.StatusOK, map[string]any{"phaseLabels": map[string]string{"scope": "Scope", "build": "Build", "review": "Review", "verify": "QA", "check": "Sign-off", "ship": "Ship"}}
		}
		return http.StatusOK, ledger.WorkResult{Readback: "intent ledger: ok"}
	})
	if _, err := captureStdout(t, func() error {
		return app.cmdLedger([]string{"verify", "widget-W1", "running", "the", "import", "on", "a", "copy"})
	}); err != nil {
		t.Fatal(err)
	}
	last := (*calls)[len(*calls)-1]
	if last["path"] != "/api/v1/ledger/work" || last["action"] != "log" || last["logType"] != "verify" || last["itemId"] != "widget-W1" || last["text"] != "running the import on a copy" {
		t.Fatalf("ledger verify sent %v", last)
	}

	out, err := captureStdout(t, func() error {
		return app.cmdLedger([]string{"phases", "--label", "verify=QA", "--label", "check=Sign-off"})
	})
	last = (*calls)[len(*calls)-1]
	labels, _ := last["labels"].(map[string]any)
	if err != nil || last["path"] != "/api/v1/ledger/phases" || labels["verify"] != "QA" || labels["check"] != "Sign-off" || !strings.Contains(out, "verify: QA\n") {
		t.Fatalf("ledger phases --label sent %v and printed %q, err %v", last, out, err)
	}

	sent := len(*calls)
	out, err = captureStdout(t, func() error { return app.cmdLedger([]string{"phases"}) })
	if err != nil || len(*calls) != sent || out != "scope: Scope\nbuild: Build\nreview: Review\nverify: Verify\ncheck: Check\nship: Ship\n" {
		t.Fatalf("ledger phases printed %q, err %v, and sent %d requests", out, err, len(*calls)-sent)
	}
	if _, err := captureStdout(t, func() error { return app.cmdLedger([]string{"phases", "--label", "verify"}) }); err == nil || !strings.Contains(err.Error(), "<phase>=<name>") {
		t.Fatalf("a label without a name: %v", err)
	}
}
