package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestLedgerProofWritesStepsAndListsThem(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(string, map[string]any) (int, any) {
		return http.StatusOK, ledger.WorkResult{Readback: "intent ledger: ok"}
	})
	add := []string{"proof", "widget-W1", "--title", "Import the March export", "--expected", "every row lands once", "--observed", "4,112 rows",
		"--outcome", "pass", "--revision", "45a6bdafb", "--link", "run log=https://ci.example.com/runs/12"}
	if _, err := captureStdout(t, func() error { return app.cmdLedger(add) }); err != nil {
		t.Fatal(err)
	}
	last := (*calls)[len(*calls)-1]
	proof, _ := last["proof"].(map[string]any)
	step, _ := proof["step"].(map[string]any)
	links, _ := step["attachments"].([]any)
	if last["path"] != "/api/v1/ledger/work" || last["action"] != "proof" || proof["op"] != "add" || proof["operationId"] == "" || step["title"] != "Import the March export" || step["outcome"] != "pass" || len(links) != 1 {
		t.Fatalf("ledger proof sent %v", last)
	}
	if _, err := captureStdout(t, func() error {
		return app.cmdLedger([]string{"proof", "widget-W1", "--remove", "--step", "p1", "--base", "2"})
	}); err != nil {
		t.Fatal(err)
	}
	proof, _ = (*calls)[len(*calls)-1]["proof"].(map[string]any)
	if removed, _ := proof["step"].(map[string]any); proof["op"] != "remove" || removed["id"] != "p1" || proof["base"] != float64(2) {
		t.Fatalf("ledger proof --remove sent %v", proof)
	}
	if _, err := captureStdout(t, func() error { return app.cmdLedger([]string{"proof", "widget-W1", "--step", "p1"}) }); err == nil || !strings.Contains(err.Error(), "--base") {
		t.Fatalf("a replace without --base: %v", err)
	}

	store := ledger.NewStore(app.registry().WriteDB(), app.registry().ReadDB())
	now := time.Now()
	for _, req := range []ledger.WorkRequest{
		{Action: ledger.WorkAdd, Text: "import the March export"},
		{Action: ledger.WorkClaim, ItemID: "widget-W1"},
		{Action: ledger.WorkProof, ItemID: "widget-W1", Proof: &ledger.ProofWrite{Op: ledger.ProofAdd, OperationID: "op-1", Step: ledger.ProofStep{Title: "Search finds it", Expected: "under a second"}}},
	} {
		req.ProjectKey, req.SessionID = "repo:github.com/acme/widget", "s1"
		if _, err := store.Work(req, now); err != nil {
			t.Fatal(err)
		}
	}
	out, err := captureStdout(t, func() error { return app.cmdLedger([]string{"proof", "widget-W1"}) })
	if err != nil || !strings.Contains(out, "widget-W1 proof: 0 passed, 0 failed, 1 pending") || !strings.Contains(out, "p1 v1 [pending] Search finds it\n  expected: under a second") {
		t.Fatalf("ledger proof printed %q, err %v", out, err)
	}
}
