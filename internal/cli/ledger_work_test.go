package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

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

func TestLedgerWorkNestsItemsUnderParentsAndShowsTheTree(t *testing.T) {
	var store *ledger.Store
	app, calls := ledgerWriteFixture(t, func(_ string, body map[string]any) (int, any) {
		data, _ := json.Marshal(body)
		var req ledger.WorkRequest
		if err := json.Unmarshal(data, &req); err != nil {
			t.Fatal(err)
		}
		result, err := store.Work(req, time.Now())
		if err != nil {
			return http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "invalid", "message": err.Error()}}
		}
		return http.StatusOK, result
	})
	store = ledger.NewStore(app.registry().WriteDB(), app.registry().ReadDB())
	for _, args := range [][]string{
		{"add", "ship", "search"},
		{"add", "schema", "--parent", "widget-W1"},
		{"add", "index", "--parent", "widget-W1"},
		{"add", "docs"},
		{"edit", "widget-W4", "--parent", "widget-W2"},
		{"dep", "widget-W4", "--on", "widget-W3"},
		{"dep", "widget-W4", "--on", "widget-W3", "--remove"},
		{"edit", "widget-W3", "--no-parent"},
	} {
		if _, err := captureStdout(t, func() error { return app.cmdLedger(args) }); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if fields, _ := (*calls)[1]["fields"].(map[string]any); fields["parent"] != "widget-W1" {
		t.Fatalf("add --parent sent %v", (*calls)[1])
	}
	if change, _ := (*calls)[7]["change"].(map[string]any); change["parent"] != "" {
		t.Fatalf("edit --no-parent sent %v", (*calls)[7])
	}
	if (*calls)[6]["action"] != "undep" {
		t.Fatalf("dep --remove sent %v", (*calls)[6])
	}
	if _, err := captureStdout(t, func() error {
		return app.cmdLedger([]string{"edit", "widget-W3", "--parent", "widget-W1", "--no-parent"})
	}); err == nil {
		t.Fatal("edit took both --parent and --no-parent")
	}
	out, err := captureStdout(t, func() error { return app.cmdLedger([]string{"tree"}) })
	if err != nil {
		t.Fatal(err)
	}
	want := "widget-W1 [open] ship search\n  widget-W2 [open] schema\n    widget-W4 [open] docs\nwidget-W3 [open] index\n"
	if out != want {
		t.Fatalf("tree printed\n%s\nwant\n%s", out, want)
	}
	out, err = captureStdout(t, func() error { return app.cmdLedger([]string{"show", "widget-W1"}) })
	if err != nil || !strings.Contains(out, "children, 0 of 1 finished:\n  widget-W2 [open]: schema") {
		t.Fatalf("show printed %q, err %v", out, err)
	}
}

func TestLedgerWorkRefusesATitleOverEightyCharactersOnAddAndEdit(t *testing.T) {
	var store *ledger.Store
	app, _ := ledgerWriteFixture(t, func(_ string, body map[string]any) (int, any) {
		data, _ := json.Marshal(body)
		var req ledger.WorkRequest
		if err := json.Unmarshal(data, &req); err != nil {
			t.Fatal(err)
		}
		result, err := store.Work(req, time.Now())
		if err != nil {
			return http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "invalid", "message": err.Error()}}
		}
		return http.StatusOK, result
	})
	store = ledger.NewStore(app.registry().WriteDB(), app.registry().ReadDB())
	fits, long := strings.Repeat("é", 80), strings.Repeat("é", 81)
	run := func(args ...string) error {
		_, err := captureStdout(t, func() error { return app.cmdLedger(args) })
		return err
	}
	if err := run("add", long); err == nil || !strings.Contains(err.Error(), "put the rest in the description") {
		t.Fatalf("add of 81 characters: %v", err)
	}
	if err := run("add", fits); err != nil {
		t.Fatalf("add of 80 characters: %v", err)
	}
	if err := run("work", "edit", "widget-W1", "--text", long); err == nil || !strings.Contains(err.Error(), "put the rest in the description") {
		t.Fatalf("edit to 81 characters: %v", err)
	}
}
