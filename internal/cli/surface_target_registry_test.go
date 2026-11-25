package cli

import (
	"context"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestASurfaceTargetResolvesFromTheRegistryBeforeAskingTheSurface(t *testing.T) {
	const id = "019f004a-0000-7000-8000-0000000000c1"
	session := surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "Release Notes", Cwd: "/work/notes", Status: surface.StatusIdle}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{id: session}}
	app, registry := cliFixture(t, fake)
	if err := registry.RecordObservedSession(session); err != nil {
		t.Fatal(err)
	}
	resolved, _, err := app.resolveLocalTarget(context.Background(), "codex:release notes")
	if err != nil || resolved == nil || resolved.ID != id {
		t.Fatalf("session=%+v err=%v", resolved, err)
	}
}

func TestNotionNewStillCreatesWhenACatalogedNameContainsNew(t *testing.T) {
	existing := surface.Session{ID: "notion-existing", Surface: surface.KindNotion, RuntimeKind: surface.RuntimeNotionWeb, Name: "new:project draft", Status: surface.StatusIdle}
	for _, selector := range []string{"new", "new:project"} {
		synthetic := surface.Session{ID: selector, Surface: surface.KindNotion, RuntimeKind: surface.RuntimeNotionWeb}
		fake := &cliSurface{kind: surface.KindNotion, sessions: map[string]surface.Session{selector: synthetic}}
		app, registry := cliFixture(t, fake)
		if err := registry.RecordObservedSession(existing); err != nil {
			t.Fatal(err)
		}
		resolved, _, err := app.resolveLocalTarget(context.Background(), "notion:"+selector)
		if err != nil || resolved == nil || resolved.ID != selector {
			t.Fatalf("selector=%q session=%+v err=%v", selector, resolved, err)
		}
	}
}
