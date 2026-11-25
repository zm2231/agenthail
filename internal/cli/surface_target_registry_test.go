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
	existing := surface.Session{ID: "notion-existing", Surface: surface.KindNotion, RuntimeKind: surface.RuntimeNotionWeb, Name: "new project", Status: surface.StatusIdle}
	synthetic := surface.Session{ID: "new", Surface: surface.KindNotion, RuntimeKind: surface.RuntimeNotionWeb}
	fake := &cliSurface{kind: surface.KindNotion, sessions: map[string]surface.Session{"new": synthetic}}
	app, registry := cliFixture(t, fake)
	if err := registry.RecordObservedSession(existing); err != nil {
		t.Fatal(err)
	}
	resolved, _, err := app.resolveLocalTarget(context.Background(), "notion:new")
	if err != nil || resolved == nil || resolved.ID != "new" {
		t.Fatalf("session=%+v err=%v", resolved, err)
	}
}
