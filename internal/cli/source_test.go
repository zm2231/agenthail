package cli

import (
	"context"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestSourceIdentityUsesExplicitSenderBeforeEnvironment(t *testing.T) {
	t.Setenv("CODEX_THREAD_ID", "older")
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

func TestSourceIdentityReadsTheClaudeConversationBeforeAnInheritedCodexThread(t *testing.T) {
	t.Setenv("CODEX_THREAD_ID", "ambient-codex")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "conversation-branch")
	claude := &cliSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{"conversation-branch": {ID: "conversation-branch", Surface: surface.KindClaude}}}
	codex := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"ambient-codex": {ID: "ambient-codex", Surface: surface.KindCodex}}}
	app := App{Surfaces: []SurfaceEntry{{Name: "claude", Surface: claude}, {Name: "codex", Surface: codex}}}
	got, err := app.sourceSessionID(context.Background(), "")
	if err != nil || got != "conversation-branch" {
		t.Fatalf("source=%q err=%v", got, err)
	}
}

func TestSourceIdentityLeavesStandaloneCLICallerUnbound(t *testing.T) {
	app := App{Surfaces: []SurfaceEntry{{Name: "claude", Surface: &cliSurface{kind: surface.KindClaude}}}}
	got, err := app.sourceSessionID(context.Background(), "")
	if err != nil || got != "" {
		t.Fatalf("source=%q err=%v", got, err)
	}
}
