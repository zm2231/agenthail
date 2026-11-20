package daemon

import (
	"testing"

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
		{surface.KindCodex, "", "deals-plan-2b", "/work/deals", "Codex agent"},
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
