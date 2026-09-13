package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
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

func TestSourceIdentityUsesRegisteredSessionWithoutOpeningItsTransport(t *testing.T) {
	t.Setenv("AGENTHAIL_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "current")
	t.Setenv("CLAUDE_SESSION_ID", "")
	store, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterSession(surface.Session{ID: "current", Surface: surface.KindCodex, Name: "voice source"}); err != nil {
		t.Fatal(err)
	}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{}}
	app := App{Registry: store, Surfaces: []SurfaceEntry{{Name: "codex", Surface: fake}}}
	got, err := app.sourceSessionID(context.Background(), "")
	if err != nil || got != "current" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}
