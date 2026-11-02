package registry

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestCoordinatorSinceFollowsEachAssignmentAndHandover(t *testing.T) {
	r := openTestRegistry(t)
	root := t.TempDir()
	if err := r.AddProject(ProjectRecord{ID: "proj", WorkspaceRoot: root, Title: "proj", Key: "proj-key"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second"} {
		if err := r.RegisterSession(surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: root, Status: surface.StatusIdle}); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, err := r.CoordinatorSince("proj", "first"); err != nil || ok {
		t.Fatalf("no coordinator yet: ok=%v err=%v", ok, err)
	}
	before := time.Now().Add(-time.Millisecond)
	spec, err := r.SetCoordinator(CoordinatorSpec{ProjectID: "proj", ProjectRoot: root, Name: "lead-proj", Surface: "claude", Cwd: root}, CoordinatorChange{SessionID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	assigned, ok, err := r.CoordinatorSince("proj", "first")
	if err != nil || !ok || assigned.Before(before.Truncate(time.Millisecond)) {
		t.Fatalf("first coordinator since %v ok=%v err=%v", assigned, ok, err)
	}
	if _, ok, _ := r.CoordinatorSince("proj", "second"); ok {
		t.Fatal("a session that is not the coordinator has a coordinator grant")
	}
	time.Sleep(5 * time.Millisecond)
	if err := r.HandOverCoordinator(spec, "first", "second"); err != nil {
		t.Fatal(err)
	}
	handed, ok, err := r.CoordinatorSince("proj", "second")
	if err != nil || !ok || !handed.After(assigned) {
		t.Fatalf("handover grant %v (assigned %v) ok=%v err=%v", handed, assigned, ok, err)
	}
	if _, ok, _ := r.CoordinatorSince("proj", "first"); ok {
		t.Fatal("the replaced conversation kept its coordinator grant")
	}
	history, err := r.CoordinatorHistory("proj")
	if err != nil {
		t.Fatal(err)
	}
	last := history[len(history)-1]
	if last.Action != "replaced" || last.SessionID != "second" || last.PreviousSessionID != "first" {
		t.Fatalf("handover not in coordinator history: %+v", last)
	}
}
