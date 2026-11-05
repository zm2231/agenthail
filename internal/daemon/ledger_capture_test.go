package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

	receipt, err := d.dispatcher().DeliverWithOptions(
		context.Background(), fake, &target,
		"remember to test the ledger", "delivery-key-1",
		surface.SendOptions{Author: surface.AuthorPerson},
	)
	if err != nil {
		t.Fatal(err)
	}

	events, err := store.Events(projectKey, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	events = withoutCapture(events)
	if len(events) != 1 {
		t.Fatalf("expected 1 ledger event, got %d", len(events))
	}
	if events[0].Type != ledger.EventUtterance {
		t.Fatalf("expected utterance event, got %s", events[0].Type)
	}
	if events[0].ActorSessionID != surface.PersonSessionID {
		t.Fatalf("expected actor %s, got %s", surface.PersonSessionID, events[0].ActorSessionID)
	}
	var payload ledger.UtterancePayload
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.TargetID != "target-session" {
		t.Fatalf("expected targetId target-session, got %s", payload.TargetID)
	}
	if payload.Surface != string(surface.KindCodex) {
		t.Fatalf("expected surface %s, got %s", surface.KindCodex, payload.Surface)
	}
	want := ledger.DeliveryRef(receipt.MessageID)
	if receipt.MessageID == "" || payload.UtteranceID != want || events[0].Ref != want {
		t.Fatalf("utterance must carry the delivery intent ref %q, got %q / %q", want, payload.UtteranceID, events[0].Ref)
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

func TestPersonSendCapturesNestedProject(t *testing.T) {
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })

	outerDir := t.TempDir()
	innerDir := filepath.Join(outerDir, "child")
	if err := os.MkdirAll(innerDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := r.AddProject(registry.ProjectRecord{
		ID: "outer", WorkspaceRoot: outerDir, Title: "Outer", Key: "outer-key",
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.AddProject(registry.ProjectRecord{
		ID: "inner", WorkspaceRoot: innerDir, Title: "Inner", Key: "inner-key",
	}); err != nil {
		t.Fatal(err)
	}

	store := ledger.NewStore(r.WriteDB(), r.ReadDB())
	if err := store.Enable("inner-key"); err != nil {
		t.Fatal(err)
	}

	target := surface.Session{
		ID: "nested-target", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop,
		Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", Cwd: innerDir,
	}
	if err := r.RegisterSession(target); err != nil {
		t.Fatal(err)
	}

	fake := &daemonSurface{
		sessions:     map[string]surface.Session{"nested-target": target},
		observations: map[string]*surface.TurnObservation{},
		accepted:     true,
	}
	d := newTestDaemon(t, r, []surface.Surface{fake})

	_, err = d.dispatcher().DeliverWithOptions(
		context.Background(), fake, &target,
		"nested project message", "",
		surface.SendOptions{Author: surface.AuthorPerson},
	)
	if err != nil {
		t.Fatal(err)
	}

	innerEvents, err := store.Events("inner-key", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	innerEvents = withoutCapture(innerEvents)
	if len(innerEvents) != 1 {
		t.Fatalf("expected 1 event for inner project, got %d", len(innerEvents))
	}

	outerEvents, err := store.Events("outer-key", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	outerEvents = withoutCapture(outerEvents)
	if len(outerEvents) != 0 {
		t.Fatalf("expected 0 events for outer project, got %d", len(outerEvents))
	}
}

func TestPersonSendSkipsNoProject(t *testing.T) {
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })

	target := surface.Session{
		ID: "orphan", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop,
		Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", Cwd: t.TempDir(),
	}
	if err := r.RegisterSession(target); err != nil {
		t.Fatal(err)
	}

	fake := &daemonSurface{
		sessions:     map[string]surface.Session{"orphan": target},
		observations: map[string]*surface.TurnObservation{},
		accepted:     true,
	}
	d := newTestDaemon(t, r, []surface.Surface{fake})

	_, err = d.dispatcher().DeliverWithOptions(
		context.Background(), fake, &target,
		"no project here", "",
		surface.SendOptions{Author: surface.AuthorPerson},
	)
	if err != nil {
		t.Fatal(err)
	}
}
