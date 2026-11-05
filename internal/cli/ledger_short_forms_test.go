package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestLedgerHelpListsEveryCommandWhereverItIsAskedFor(t *testing.T) {
	app, _ := ledgerWriteFixture(t, func(string, map[string]any) (int, any) { return http.StatusOK, nil })
	for _, args := range [][]string{{"--help"}, {"help"}, {"-h"}, {"work", "--help"}} {
		out, err := captureStdout(t, func() error { return app.cmdLedger(args) })
		if err != nil || out != ledgerHelp {
			t.Fatalf("ledger %v printed %q, err %v", args, out, err)
		}
	}
	if out, err := captureStdout(t, func() error { return app.Run([]string{"help", "ledger"}) }); err != nil || out != ledgerHelp {
		t.Fatalf("help ledger printed %q, err %v", out, err)
	}
	_, err := captureStdout(t, func() error { return app.cmdLedger([]string{"frobnicate"}) })
	if err == nil || !strings.Contains(err.Error(), `unknown ledger command "frobnicate"`) || !strings.Contains(err.Error(), ledgerHelp) {
		t.Fatalf("an unknown command does not list the commands: %v", err)
	}
	named := func(command string) bool {
		return strings.Contains(ledgerHelp, "ledger "+command+" ") || strings.Contains(ledgerHelp, "ledger "+command+"\n") ||
			strings.Contains(ledgerHelp, "("+command+")") || strings.Contains(ledgerHelp, "("+command+",") || strings.Contains(ledgerHelp, ", "+command+")")
	}
	for command := range ledgerCommands {
		if !named(command) {
			t.Errorf("ledger %s runs but the help does not name it", command)
		}
	}
	for command := range ledgerWorkShortForms {
		if !named(command) {
			t.Errorf("ledger %s runs but the help does not name it", command)
		}
	}
}

func TestLedgerHelpIsAFlagNotAValue(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(string, map[string]any) (int, any) {
		return http.StatusOK, ledger.WorkResult{Readback: "intent ledger: ok"}
	})
	out, err := captureStdout(t, func() error { return app.cmdLedger([]string{"add", "explain", "--help", "--description", "--help"}) })
	if err != nil || out != ledgerHelp || len(*calls) != 0 {
		t.Fatalf("a --help flag printed %q, err %v, and sent %d requests", out, err, len(*calls))
	}
	if _, err := captureStdout(t, func() error { return app.cmdLedger([]string{"add", "explain", "the", "flags", "--description", "--help"}) }); err != nil {
		t.Fatal(err)
	}
	fields, _ := (*calls)[0]["fields"].(map[string]any)
	if len(*calls) != 1 || (*calls)[0]["text"] != "explain the flags" || fields["description"] != "--help" {
		t.Fatalf("--help as a value did not reach the write: %v", *calls)
	}
}

func TestLedgerTakesTDStyleShortFormsForWorkItems(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(string, map[string]any) (int, any) {
		return http.StatusOK, ledger.WorkResult{Readback: "intent ledger: ok"}
	})
	cases := []struct {
		args   []string
		action string
	}{
		{[]string{"start", "widget-W1"}, "claim"},
		{[]string{"review", "widget-W1"}, "submit"},
		{[]string{"close", "widget-W1", "--reason", "wontfix"}, "close"},
		{[]string{"delete", "widget-W1"}, "delete"},
		{[]string{"reopen", "widget-W1"}, "reopen"},
		{[]string{"comment", "widget-W1", "seen", "again"}, "comment"},
		{[]string{"update", "widget-W1", "--priority", "P0"}, "edit"},
		{[]string{"work", "start", "widget-W1"}, "claim"},
		{[]string{"--project", "list", "close", "widget-W1", "--reason", "obsolete"}, "close"},
		{[]string{"--reason", "show", "close", "widget-W1"}, "close"},
	}
	for _, c := range cases {
		if _, err := captureStdout(t, func() error { return app.cmdLedger(c.args) }); err != nil {
			t.Fatalf("ledger %v: %v", c.args, err)
		}
		last := (*calls)[len(*calls)-1]
		if last["action"] != c.action || last["itemId"] != "widget-W1" {
			t.Fatalf("ledger %v sent %v, want action %s", c.args, last, c.action)
		}
	}

	store := ledger.NewStore(app.registry().WriteDB(), app.registry().ReadDB())
	if _, err := store.Work(ledger.WorkRequest{ProjectKey: "repo:github.com/acme/widget", SessionID: "s1", Action: ledger.WorkAdd, Text: "the index leaks",
		Fields: ledger.WorkFields{Type: ledger.WorkBug}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"show", "widget-W1"}, {"work", "show", "widget-W1"}} {
		out, err := captureStdout(t, func() error { return app.cmdLedger(args) })
		if err != nil || !strings.Contains(out, "widget-W1 [open] P2 bug found: the index leaks") || !strings.Contains(out, "timeline:") {
			t.Fatalf("ledger %v printed %q, err %v", args, out, err)
		}
	}
	for _, args := range [][]string{{"list"}, {"work", "list"}, {"list", "--type", "bug"}, {"--type", "bug", "list"}} {
		out, err := captureStdout(t, func() error { return app.cmdLedger(args) })
		if err != nil || !strings.Contains(out, "widget-W1 [open]") {
			t.Fatalf("ledger %v printed %q, err %v", args, out, err)
		}
	}
	if _, err := captureStdout(t, func() error { return app.cmdLedger([]string{"show", "widget-W9"}) }); err == nil || !strings.Contains(err.Error(), "not a work item or an ask") {
		t.Fatalf("show of an unknown id: %v", err)
	}
}
