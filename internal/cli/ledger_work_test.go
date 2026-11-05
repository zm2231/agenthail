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
		{"work", "add", "add", "the", "index", "--clause", "widget-C1"},
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
	if _, err := captureStdout(t, func() error { return app.cmdLedger([]string{"work", "add", "--clause", "widget-C1"}) }); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("add without text: %v", err)
	}
}

func TestLedgerWorkRecordsFoundWorkAndItsFields(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(string, map[string]any) (int, any) {
		return http.StatusOK, ledger.WorkResult{Readback: "intent ledger: ok"}
	})
	for _, args := range [][]string{
		{"create", "index", "leaks", "a", "handle", "--type", "bug", "--priority", "P1", "--found-in", "widget-W1", "--label", "index", "--label", "perf", "--description", "seen in CI"},
		{"add", "tidy", "the", "logs"},
		{"work", "edit", "widget-W2", "--priority", "P0", "--unlabel", "perf", "--text", "index leaks a file handle"},
		{"work", "link", "widget-W2", "widget-C1"},
		{"work", "close", "widget-W3", "--reason", "duplicate of widget-W2"},
	} {
		if _, err := captureStdout(t, func() error { return app.cmdLedger(args) }); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	got := *calls
	fields, _ := got[0]["fields"].(map[string]any)
	if got[0]["action"] != "add" || got[0]["clauseId"] != nil || got[0]["text"] != "index leaks a handle" || fields["type"] != "bug" || fields["priority"] != "P1" || fields["from"] != "widget-W1" || fields["description"] != "seen in CI" {
		t.Fatalf("create request %v", got[0])
	}
	if labels, _ := fields["labels"].([]any); len(labels) != 2 {
		t.Fatalf("create labels %v", fields["labels"])
	}
	if got[1]["action"] != "add" || got[1]["text"] != "tidy the logs" {
		t.Fatalf("add request %v", got[1])
	}
	change, _ := got[2]["change"].(map[string]any)
	if got[2]["action"] != "edit" || change["priority"] != "P0" || change["text"] != "index leaks a file handle" || change["type"] != nil || got[2]["logType"] != nil {
		t.Fatalf("edit request %v", got[2])
	}
	if got[3]["action"] != "link" || got[3]["itemId"] != "widget-W2" || got[3]["clauseId"] != "widget-C1" {
		t.Fatalf("link request %v", got[3])
	}
	if got[4]["action"] != "close" || got[4]["reason"] != "duplicate of widget-W2" {
		t.Fatalf("close request %v", got[4])
	}
}
