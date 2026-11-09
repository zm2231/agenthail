package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestTheBoardListsAutoFiledMessagesUnderTheirGoalAndCountsOverrides(t *testing.T) {
	app, store := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	dir := t.TempDir()
	const key = "repo:github.com/acme/widget"
	if err := store.AddProject(registry.ProjectRecord{ID: "proj", WorkspaceRoot: dir, Title: "proj", Key: key}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	ls := ledger.NewStore(store.WriteDB(), store.ReadDB())
	search, _, _ := ls.EnsureGoal(key, "Search", "s1")
	billing, _, _ := ls.EnsureGoal(key, "Billing", "s1")
	for _, u := range []ledger.Utterance{
		{ProjectKey: key, Ref: "claude-prompt:1", SpeakerID: "person", TargetID: "s1", Text: "rank exact   matches first", CapturedBy: ledger.CapturedByHook},
		{ProjectKey: key, Ref: "claude-prompt:2", SpeakerID: "person", TargetID: "s1", Text: "send receipts", CapturedBy: ledger.CapturedByHook},
		{ProjectKey: key, Ref: "claude-prompt:3", SpeakerID: "person", TargetID: "s1", Text: "thanks", CapturedBy: ledger.CapturedByHook},
	} {
		if _, _, err := ls.RecordUtterance(u); err != nil {
			t.Fatal(err)
		}
	}
	for _, ref := range []string{"claude-prompt:1", "claude-prompt:2"} {
		if _, err := ls.RecordSort(key, ledger.SortedPayload{UtteranceID: ref, GoalID: search.ID, Confidence: 0.93, Model: "d1-3b"}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ls.Attach(ledger.AttachRequest{ProjectKey: key, SessionID: "s1", UtteranceID: "claude-prompt:2", As: ledger.AsNewAsk, To: billing.ID, Clauses: []string{"send receipts"}}); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error { return app.cmdLedger(nil) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"widget-G1 Search\n  filed automatically (0.93) claude-prompt:1 to s1: rank exact matches first\n",
		"auto-filed 2, overridden 1\n",
		"provisional until attached (1):\n  claude-prompt:3 to s1: thanks",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("board lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "filed automatically (0.93) claude-prompt:2") {
		t.Fatalf("an overridden sort is still listed under its goal:\n%s", out)
	}
}
