package cli

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestCriticalPathReadsTheRequestedProject(t *testing.T) {
	app, store := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	dir := t.TempDir()
	const here, other = "repo:github.com/acme/widget", "repo:github.com/acme/infra"
	if err := store.AddProject(registry.ProjectRecord{ID: "proj", WorkspaceRoot: dir, Title: "proj", Key: here}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	ls := ledger.NewStore(store.WriteDB(), store.ReadDB())
	u := ledger.Utterance{ProjectKey: other, Ref: "claude-prompt:1", SpeakerID: "person", TargetID: "s1", Text: "rotate the keys", At: time.Now(), CapturedBy: ledger.CapturedByHook}
	if _, _, err := ls.RecordUtterance(u); err != nil {
		t.Fatal(err)
	}
	g, _, err := ls.EnsureGoal(other, "Keys", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ls.Attach(ledger.AttachRequest{ProjectKey: other, SessionID: "s1", UtteranceID: u.Ref, As: ledger.AsNewAsk, To: g.ID, Clauses: []string{"rotate the keys"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ls.Work(ledger.WorkRequest{ProjectKey: other, SessionID: "s1", Action: ledger.WorkAdd, ClauseID: "infra-C1", Text: "new key pair"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	path := func(args ...string) []ledger.WorkItem {
		t.Helper()
		out, err := captureStdout(t, func() error { return app.cmdLedger(append([]string{"work", "critical-path", "--json"}, args...)) })
		if err != nil {
			t.Fatal(err)
		}
		var items []ledger.WorkItem
		if err := json.Unmarshal([]byte(out), &items); err != nil {
			t.Fatalf("%q: %v", out, err)
		}
		return items
	}
	if items := path("--project", other); len(items) != 1 || items[0].ID != "infra-W1" {
		t.Fatalf("critical path of %s = %+v", other, items)
	}
	if items := path(); len(items) != 0 {
		t.Fatalf("critical path of the cwd project = %+v", items)
	}
}
