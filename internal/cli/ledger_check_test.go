package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestCheckCommandsPostWhatTheCheckerAndTheProposerSay(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(path string, _ map[string]any) (int, any) {
		switch path {
		case "/api/v1/ledger/check-report":
			return http.StatusOK, ledger.CheckOutcome{GoalID: "widget-G1", Result: ledger.CheckReported, Cuts: []string{"widget-X1"}}
		case "/api/v1/ledger/check":
			return http.StatusOK, map[string]any{"goalId": "widget-G1", "queued": true}
		}
		return http.StatusOK, ledger.AttachResult{Readback: "intent ledger: ok"}
	})
	run := func(args ...string) string {
		t.Helper()
		out, err := captureStdout(t, func() error { return app.cmdLedger(args) })
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out
	}
	stdinHolding(t, "")
	t.Setenv("AGENTHAIL_CHECK_TOKEN", "run-secret")
	if _, err := captureStdout(t, func() error { return app.cmdLedger([]string{"check-report", "run-1", "--none"}) }); err == nil || !strings.Contains(err.Error(), "only the checker") {
		t.Fatalf("check-report without the run token on stdin: %v", err)
	}
	stdinHolding(t, "run-secret\n")
	if out := run("check-report", "run-1", "--missing", "typo tolerance", "--in", "claude-prompt:2", "--missing", "csv export", "--in", "claude-prompt:2"); !strings.Contains(out, "pending cuts widget-X1") {
		t.Fatalf("check-report output %q", out)
	}
	run("check", "widget-G1")
	run("attach", "claude-prompt:2", "--as", "extends", "--to", "widget-A1", "--clause", "typo tolerance", "--covers", "widget-X1")
	got := *calls
	report, _ := json.Marshal(got[0]["missing"])
	if got[0]["run"] != "run-1" || got[0]["token"] != "run-secret" || got[0]["sessionId"] != nil || string(report) != `[{"breadth":"typo tolerance","from":"claude-prompt:2"},{"breadth":"csv export","from":"claude-prompt:2"}]` {
		t.Fatalf("check-report request %v", got[0])
	}
	if got[1]["path"] != "/api/v1/ledger/check" || got[1]["goalId"] != "widget-G1" || got[1]["sessionId"] != "s1" {
		t.Fatalf("check request %v", got[1])
	}
	if covers, _ := json.Marshal(got[2]["covers"]); string(covers) != `["widget-X1"]` {
		t.Fatalf("attach request %v", got[2])
	}
	for _, args := range [][]string{
		{"check-report", "run-1"},
		{"check-report", "run-1", "--none", "--missing", "x", "--in", "u"},
		{"check-report", "run-1", "--missing", "x"},
		{"check-report", "--none"},
	} {
		if _, err := captureStdout(t, func() error { return app.cmdLedger(args) }); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func stdinHolding(t *testing.T, text string) {
	t.Helper()
	stdin, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	previous := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() { os.Stdin = previous; stdin.Close() })
}
