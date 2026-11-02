package daemon

import (
	"context"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestPersonSendCapturesUtterance(t *testing.T) {
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })

	projectDir := t.TempDir()
	projectKey := "test-project-key"
	if err := r.AddProject(registry.ProjectRecord{
		ID:            "proj-1",
		WorkspaceRoot: projectDir,
		Title:         "Test Project",
		Key:           projectKey,
	}); err != nil {
		t.Fatal(err)
	}

	store := ledger.NewStore(r.WriteDB(), r.ReadDB())
	if err := store.Enable(projectKey); err != nil {
		t.Fatal(err)
	}

	target := surface.Session{
		ID:          "target-session",
		Surface:     surface.KindCodex,
		RuntimeKind: surface.RuntimeCodexDesktop,
		Status:      surface.StatusIdle,
		Source:      "vscode",
		Transport:   "desktop",
		Cwd:         projectDir,
	}
	if err := r.RegisterSession(target); err != nil {
		t.Fatal(err)
	}

	fake := &daemonSurface{
		sessions:     map[string]surface.Session{"target-session": target},
		observations: map[string]*surface.TurnObservation{},
		accepted:     true,
	}
	d := newTestDaemon(t, r, []surface.Surface{fake})

	_, err = d.dispatcher().DeliverWithOptions(
		context.Background(), fake, &target,
		"remember to test the ledger", "",
		surface.SendOptions{Author: surface.AuthorPerson},
	)
	if err != nil {
		t.Fatal(err)
	}

	events, err := store.Events(projectKey, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 ledger event, got %d", len(events))
	}
	if events[0].Type != ledger.EventUtterance {
		t.Fatalf("expected utterance event, got %s", events[0].Type)
	}
	if events[0].ActorSessionID != surface.PersonSessionID {
		t.Fatalf("expected actor %s, got %s", surface.PersonSessionID, events[0].ActorSessionID)
	}
}

func TestPersonSendSkipsDisabledLedger(t *testing.T) {
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })

	projectDir := t.TempDir()
	projectKey := "disabled-project"
	if err := r.AddProject(registry.ProjectRecord{
		ID:            "proj-2",
		WorkspaceRoot: projectDir,
		Title:         "Disabled Project",
		Key:           projectKey,
	}); err != nil {
		t.Fatal(err)
	}

	target := surface.Session{
		ID:          "target-2",
		Surface:     surface.KindCodex,
		RuntimeKind: surface.RuntimeCodexDesktop,
		Status:      surface.StatusIdle,
		Source:      "vscode",
		Transport:   "desktop",
		Cwd:         projectDir,
	}
	if err := r.RegisterSession(target); err != nil {
		t.Fatal(err)
	}

	fake := &daemonSurface{
		sessions:     map[string]surface.Session{"target-2": target},
		observations: map[string]*surface.TurnObservation{},
		accepted:     true,
	}
	d := newTestDaemon(t, r, []surface.Surface{fake})

	_, err = d.dispatcher().DeliverWithOptions(
		context.Background(), fake, &target,
		"this should not be captured", "",
		surface.SendOptions{Author: surface.AuthorPerson},
	)
	if err != nil {
		t.Fatal(err)
	}

	store := ledger.NewStore(r.WriteDB(), r.ReadDB())
	events, err := store.Events(projectKey, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 ledger events for disabled project, got %d", len(events))
	}
}
