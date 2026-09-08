package cli

import (
	"context"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestSourceIdentityUsesExplicitSenderBeforeEnvironment(t *testing.T) {
	t.Setenv("AGENTHAIL_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "older")
	t.Setenv("CLAUDE_SESSION_ID", "")
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"older": {ID: "older", Surface: surface.KindCodex}, "explicit": {ID: "explicit", Surface: surface.KindCodex}}}
	app := App{Surfaces: []SurfaceEntry{{Name: "codex", Surface: fake}}}
	for selector, want := range map[string]string{"": "older", "codex:explicit": "explicit"} {
		got, err := app.sourceSessionID(context.Background(), selector)
		if err != nil || got != want {
			t.Fatalf("selector=%q got=%q err=%v", selector, got, err)
		}
	}
	if _, err := app.sourceSessionID(context.Background(), "codex:missing"); err == nil {
		t.Fatal("unresolvable sender accepted")
	}
}
