package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestLedgerWorkCommentAndLogPost(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(string, map[string]any) (int, any) {
		return http.StatusOK, ledger.WorkResult{Readback: "intent ledger: ok"}
	})
	for _, args := range [][]string{
		{"work", "comment", "widget-W1", "check", "the", "migration"},
		{"work", "log", "widget-W1", "tried", "fts5", "--type", "tried"},
	} {
		if _, err := captureStdout(t, func() error { return app.cmdLedger(args) }); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	got := *calls
	if got[0]["action"] != "comment" || got[0]["itemId"] != "widget-W1" || got[0]["text"] != "check the migration" || got[0]["path"] != "/api/v1/ledger/work" {
		t.Fatalf("comment request %v", got[0])
	}
	if got[1]["action"] != "log" || got[1]["text"] != "tried fts5" || got[1]["logType"] != "tried" {
		t.Fatalf("log request %v", got[1])
	}
	if _, err := captureStdout(t, func() error { return app.cmdLedger([]string{"work", "comment", "widget-W1"}) }); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("comment without text: %v", err)
	}
}

func TestLedgerWorkShowsAnItemAndWhatIsReady(t *testing.T) {
	app, store := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	dir := t.TempDir()
	const key = "repo:github.com/acme/widget"
	if err := store.AddProject(registry.ProjectRecord{ID: "proj", WorkspaceRoot: dir, Title: "proj", Key: key}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	ls := ledger.NewStore(store.WriteDB(), store.ReadDB())
	u := ledger.Utterance{ProjectKey: key, Ref: "claude-prompt:1", SpeakerID: "person", TargetID: "s1", Text: "build search", At: time.Now(), CapturedBy: ledger.CapturedByHook}
	if _, _, err := ls.RecordUtterance(u); err != nil {
		t.Fatal(err)
	}
	g, _, err := ls.EnsureGoal(key, "Search", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ls.Attach(ledger.AttachRequest{ProjectKey: key, SessionID: "s1", UtteranceID: u.Ref, As: ledger.AsNewAsk, To: g.ID, Clauses: []string{"build search"}}); err != nil {
		t.Fatal(err)
	}
	for _, req := range []ledger.WorkRequest{
		{Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: "add the index"},
		{Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: "wire the endpoint"},
		{Action: ledger.WorkDep, ItemID: "widget-W2", DependsOn: "widget-W1"},
		{Action: ledger.WorkClaim, ItemID: "widget-W1"},
		{Action: ledger.WorkComment, ItemID: "widget-W1", SessionID: "reviewer", Text: "check the migration"},
		{Action: ledger.WorkLog, ItemID: "widget-W1", Text: "fts5 is too slow", LogType: "blocker"},
	} {
		req.ProjectKey = key
		if req.SessionID == "" {
			req.SessionID = "s1"
		}
		if _, err := ls.Work(req, time.Now()); err != nil {
			t.Fatalf("%+v: %v", req, err)
		}
	}
	run := func(args ...string) string {
		t.Helper()
		out, err := captureStdout(t, func() error { return app.cmdLedger(append([]string{"work"}, args...)) })
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out
	}
	text := run("widget-W1")
	for _, want := range []string{"widget-W1 [in_progress]", "(1 comments, 1 log entries)", "timeline:", "s1  created  add the index", "s1  -> in_progress", "reviewer  comment  check the migration", "s1  log (blocker)  fts5 is too slow"} {
		if !strings.Contains(text, want) {
			t.Fatalf("ledger work widget-W1 lacks %q:\n%s", want, text)
		}
	}
	var item ledger.WorkItem
	if err := json.Unmarshal([]byte(run("widget-W1", "--json")), &item); err != nil || len(item.Timeline) != 4 || item.Timeline[3].LogType != "blocker" {
		t.Fatalf("json item = %+v, %v", item, err)
	}
	if out := run("ready"); !strings.Contains(out, "no work item is ready") {
		t.Fatalf("ready with W1 claimed and W2 waiting on it:\n%s", out)
	}
	if _, err := ls.Work(ledger.WorkRequest{ProjectKey: key, SessionID: "s1", Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: "write docs"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	var next ledger.WorkItem
	if err := json.Unmarshal([]byte(run("next", "--json")), &next); err != nil || next.ID != "widget-W3" {
		t.Fatalf("next = %+v, %v", next, err)
	}
	if _, err := captureStdout(t, func() error { return app.cmdLedger([]string{"work", "widget-W9"}) }); err == nil || !strings.Contains(err.Error(), "not a work item") {
		t.Fatalf("unknown item: %v", err)
	}
}
