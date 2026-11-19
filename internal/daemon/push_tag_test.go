package daemon

import (
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAPushIsTitledWithTheAgentsTagAndNeverItsPromptTitle(t *testing.T) {
	for _, test := range []struct {
		kind        surface.SurfaceKind
		alias, name string
		want        string
	}{
		{surface.KindClaude, "builder", "news-fqs-pilot-21", "@builder"},
		{surface.KindClaude, "", "news-fqs-pilot-21", "news-fqs-pilot-21"},
		{surface.KindClaude, "", "List files and examine the environment", "Claude agent"},
		{surface.KindCodex, "", "Fix the login bug", "Codex agent"},
		{surface.KindCodex, "", "", "Codex agent"},
		{surface.KindNotion, "", "Draft", "Notion agent"},
		{surface.KindClaude, "", "a-very-long-generated-name-that-runs-past-forty-characters", "Claude agent"},
	} {
		if got := pushTag(test.kind, test.alias, test.name); got != test.want {
			t.Errorf("pushTag(%s, %q, %q) = %q, want %q", test.kind, test.alias, test.name, got, test.want)
		}
	}
}
