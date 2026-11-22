package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestLedgerStatusShowsTheCallersWorkOrANamedSessions(t *testing.T) {
	dir := t.TempDir()
	sessions := map[string]surface.Session{}
	for _, id := range []string{"s1", "s2"} {
		sessions[id] = surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: dir, Status: surface.StatusIdle}
	}
	app, store := cliFixture(t, &cliSurface{kind: surface.KindClaude, sessions: sessions})
	const key = "repo:github.com/acme/widget"
	if err := store.AddProject(registry.ProjectRecord{ID: "proj", WorkspaceRoot: dir, Title: "proj", Key: key}); err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		if err := store.RecordObservedSession(s); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	t.Setenv("AGENTHAIL_SESSION_ID", "s1")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "")
	ls := ledger.NewStore(store.WriteDB(), store.ReadDB())
	for _, req := range []ledger.WorkRequest{
		{SessionID: "s1", Action: ledger.WorkAdd, Text: "add the index"},
		{SessionID: "s1", Action: ledger.WorkAdd, Text: "wire the endpoint"},
		{SessionID: "s1", Action: ledger.WorkAdd, Text: "write the docs"},
		{SessionID: "s1", Action: ledger.WorkClaim, ItemID: "widget-W1"},
		{SessionID: "s1", Action: ledger.WorkLog, ItemID: "widget-W1", Text: "fts5 builds in 2s"},
		{SessionID: "s1", Action: ledger.WorkClaim, ItemID: "widget-W2"},
		{SessionID: "s1", Action: ledger.WorkSubmit, ItemID: "widget-W2"},
		{SessionID: "s2", Action: ledger.WorkReject, ItemID: "widget-W2", Reason: "no test for an empty query"},
		{SessionID: "s3", Action: ledger.WorkAdd, Text: "tune the tokenizer", Fields: ledger.WorkFields{Parent: "widget-W1"}},
		{SessionID: "s3", Action: ledger.WorkClaim, ItemID: "widget-W4"},
	} {
		req.ProjectKey = key
		if _, err := ls.Work(req, time.Now()); err != nil {
			t.Fatalf("%+v: %v", req, err)
		}
	}
	run := func(args ...string) string {
		t.Helper()
		out, err := captureStdout(t, func() error { return app.cmdLedger(args) })
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out
	}
	text := run("status")
	for _, want := range []string{
		"status of s1",
		"rejected, back to you:\n  widget-W2 [in_progress] P2 wire the endpoint\n    no log yet\n    s2 sent it back: no test for an empty query",
		"holding:\n  widget-W1 [in_progress] P2 add the index\n    last log (progress, ",
		"): fts5 builds in 2s\n    children, 0 of 1 finished\n      widget-W4 [in_progress] tune the tokenizer assignee=s3\n",
		"ready to take next:\n  widget-W3 [open] P2 write the docs",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("ledger status lacks %q:\n%s", want, text)
		}
	}
	if current := run("current"); current != text {
		t.Fatalf("ledger current printed\n%s\nwant the same as ledger status:\n%s", current, text)
	}
	var theirs ledger.SessionStatus
	if err := json.Unmarshal([]byte(run("status", "s2", "--json")), &theirs); err != nil || theirs.SessionID != "s2" || len(theirs.Holding)+len(theirs.Rejected) != 0 {
		t.Fatalf("ledger status s2 --json = %+v, %v", theirs, err)
	}
	if text := run("status", "s2"); !strings.Contains(text, "status of s2\nholds no work\n") {
		t.Fatalf("ledger status s2 printed:\n%s", text)
	}
	if _, err := captureStdout(t, func() error { return app.cmdLedger([]string{"status", "s1", "s2"}) }); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("two sessions: %v", err)
	}
}
