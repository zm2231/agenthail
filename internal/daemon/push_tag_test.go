package daemon

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAPushIsTitledWithTheAgentsTagAndNeverItsPromptTitle(t *testing.T) {
	for _, test := range []struct {
		kind             surface.SurfaceKind
		alias, name, cwd string
		want             string
	}{
		{surface.KindClaude, "builder", "news-fqs-pilot-b9", "/work/news-fqs-pilot", "@builder"},
		{surface.KindClaude, "", "news-fqs-pilot-b9", "/work/news-fqs-pilot", "news-fqs-pilot-b9"},
		{surface.KindClaude, "", "vps-mangagement-77", "/work/VPS-Mangagement", "vps-mangagement-77"},
		{surface.KindClaude, "", "2026-11-18-mississippi-inactive-voter-sp-8c", "/work/2026-11-18-mississippi-inactive-voter-spike", "2026-11-18-mississippi-inactive-voter-sp-8c"},
		{surface.KindClaude, "", "List files and examine the environment", "/work/env", "Claude agent"},
		{surface.KindCodex, "", "acquisition-plans", "/work/deals", "Codex agent"},
		{surface.KindCodex, "", "deals-ab", "/work/deals", "Codex agent"},
		{surface.KindClaude, "", "agenthail-collab", "/work/agenthail", "Claude agent"},
		{surface.KindClaude, "", "news-fqs-pilot-b9", "", "Claude agent"},
		{surface.KindCodex, "", "", "/work/deals", "Codex agent"},
		{surface.KindNotion, "", "Draft", "", "Notion agent"},
	} {
		if got := pushTag(test.kind, test.alias, test.name, test.cwd); got != test.want {
			t.Errorf("pushTag(%s, %q, %q, %q) = %q, want %q", test.kind, test.alias, test.name, test.cwd, got, test.want)
		}
	}
}

func TestARowsTurnCompletionKeepsItsOwnHeadlineAfterALaterTurnEnds(t *testing.T) {
	d, r, fake, from, _ := daemonFixture(t)
	newPushRecorder(t, r)
	runTurn(t, d, fake, &from, "turn-1", "First pass done", "Done.")
	d.background.wait()
	if _, err := r.EndTurn(registry.TurnEnd{SessionID: from.ID, Headline: "Second pass done", Source: registry.TurnSourceTool}, "turn-2", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := r.BindTurnCompletion(from.ID, "turn-2", time.Now(), 0); err != nil {
		t.Fatal(err)
	}
	row := viewRow(t, d, from.ID)
	if row.TurnCompletion == nil || row.TurnCompletion.TurnID != "turn-1" || row.TurnCompletion.Headline != "First pass done" {
		t.Fatalf("the row's turn completion %+v", row.TurnCompletion)
	}
}
