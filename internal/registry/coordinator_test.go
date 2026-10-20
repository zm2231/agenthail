package registry

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func coordinatorRegistry(t *testing.T, sessions ...string) *Registry {
	t.Helper()
	r, err := Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	for _, id := range sessions {
		if err := r.RegisterSession(surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle, Cwd: "/work"}); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestAConversationACoordinatorReplacedCanCoordinateAnotherProject(t *testing.T) {
	r := coordinatorRegistry(t, "old", "new")
	alpha := CoordinatorSpec{ProjectID: "project-alpha", ProjectRoot: "/work/alpha", Name: "alpha-lead", Surface: "claude", Cwd: "/work/alpha"}
	beta := CoordinatorSpec{ProjectID: "project-beta", ProjectRoot: "/work/alpha/beta", Name: "beta-lead", Surface: "claude", Cwd: "/work/alpha/beta"}
	if _, err := r.SetCoordinator(beta, CoordinatorChange{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetCoordinator(alpha, CoordinatorChange{SessionID: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := r.HandOverCoordinator(alpha, "old", "new"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetCoordinator(beta, CoordinatorChange{SessionID: "old", Replace: true}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		spec, found, err := r.CoordinatorOfSession("old")
		if err != nil || !found || spec.ProjectID != beta.ProjectID {
			t.Fatalf("old belongs to %+v found=%v err=%v", spec, found, err)
		}
	}
	if spec, found, err := r.CoordinatorOfSession("new"); err != nil || !found || spec.ProjectID != alpha.ProjectID {
		t.Fatalf("new belongs to %+v found=%v err=%v", spec, found, err)
	}
}

func TestHandOverLeavesNoSelfOrDuplicateRelayRoute(t *testing.T) {
	r := coordinatorRegistry(t, "previous", "next", "worker", "helper")
	spec := CoordinatorSpec{ProjectID: "project-work", ProjectRoot: "/work", Name: "work-lead", Surface: "claude", Cwd: "/work"}
	if _, err := r.SetCoordinator(spec, CoordinatorChange{SessionID: "previous"}); err != nil {
		t.Fatal(err)
	}
	for _, route := range [][2]string{{"previous", "next"}, {"previous", "worker"}, {"next", "worker"}, {"helper", "previous"}} {
		if _, err := r.AddRoute(route[0], route[1], ".*"); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.HandOverCoordinator(spec, "previous", "next"); err != nil {
		t.Fatal(err)
	}
	routes, err := r.ListRoutes()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[[2]string]bool{}
	for _, route := range routes {
		key := [2]string{route.FromSession, route.ToSession}
		if route.FromSession == route.ToSession || seen[key] || route.FromSession == "previous" || route.ToSession == "previous" {
			t.Fatalf("routes=%+v", routes)
		}
		seen[key] = true
	}
	if !seen[[2]string{"next", "worker"}] || !seen[[2]string{"helper", "next"}] || len(routes) != 2 {
		t.Fatalf("routes=%+v", routes)
	}
}

func TestSetCoordinatorRefusesATakeoverUnlessReplacedAndKeepsHistory(t *testing.T) {
	r := coordinatorRegistry(t, "first", "second")
	spec := CoordinatorSpec{ProjectID: "repo:github.com/example/widget", ProjectRoot: "/work/widget", Name: "widget-lead", Surface: "claude", Cwd: "/work/widget"}
	set, err := r.SetCoordinator(spec, CoordinatorChange{SessionID: "first", ChangedBy: "mac::first"})
	if err != nil || set.SessionID != "first" || set.ChangedBy != "mac::first" {
		t.Fatalf("set=%+v err=%v", set, err)
	}
	var exists CoordinatorExistsError
	if _, err := r.SetCoordinator(spec, CoordinatorChange{SessionID: "second", ChangedBy: "mac::second"}); !errors.As(err, &exists) || len(exists.Existing) != 1 || exists.Existing[0].Name != "widget-lead" {
		t.Fatalf("takeover err=%v", err)
	}
	if current, _ := r.Coordinator(spec.ProjectID); current.SessionID != "first" {
		t.Fatalf("a refused takeover moved the coordinator: %+v", current)
	}
	replaced, err := r.SetCoordinator(spec, CoordinatorChange{SessionID: "second", Replace: true, ChangedBy: "mac::second"})
	if err != nil || replaced.SessionID != "second" {
		t.Fatalf("replace=%+v err=%v", replaced, err)
	}
	if _, err := r.ClearCoordinator(spec.ProjectID, "mac::second", set.ChangedAt.Add(-time.Second)); !errors.Is(err, ErrCoordinatorChanged) {
		t.Fatalf("stale clear err=%v", err)
	}
	if _, err := r.ClearCoordinator(spec.ProjectID, "mac::second", replaced.ChangedAt); err != nil {
		t.Fatal(err)
	}
	history, err := r.CoordinatorHistory(spec.ProjectID)
	if err != nil || len(history) != 3 {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	if history[0].Action != "set" || history[0].SessionID != "first" || history[0].ChangedBy != "mac::first" ||
		history[1].Action != "replaced" || history[1].SessionID != "second" || history[1].PreviousSessionID != "first" ||
		history[2].Action != "cleared" || history[2].PreviousSessionID != "second" || history[2].ChangedBy != "mac::second" {
		t.Fatalf("history=%+v", history)
	}
	events, err := r.CatalogEventsAfter(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, event := range events.Events {
		if strings.HasPrefix(event.Type, "coordinator.") {
			types = append(types, event.Type)
		}
	}
	if strings.Join(types, ",") != "coordinator.changed,coordinator.changed,coordinator.removed" {
		t.Fatalf("events=%v", types)
	}
}

func TestAPairedHostsCoordinatorHoldsItsProjectAndNameUntilTheHostIsRemoved(t *testing.T) {
	r := coordinatorRegistry(t, "local")
	hostID := "h0123456789ab"
	if _, err := r.AddHost(PairedHost{ID: hostID, Name: "forge", Endpoint: "https://forge.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_secret"}); err != nil {
		t.Fatal(err)
	}
	remote := CoordinatorEntry{ProjectID: "repo:github.com/example/widget", ProjectRoot: "/home/forge/widget", Name: "widget-lead", Surface: "codex", SessionID: "thread-1", ChangedBy: hostID + "::thread-1", ChangedAt: time.UnixMilli(1_700_000_000_000)}
	if err := r.ReplaceHostCoordinators(hostID, []CoordinatorEntry{remote}); err != nil {
		t.Fatal(err)
	}
	spec := CoordinatorSpec{ProjectID: remote.ProjectID, ProjectRoot: "/work/widget", Name: "mac-lead", Surface: "claude", Cwd: "/work/widget"}
	var exists CoordinatorExistsError
	for _, replace := range []bool{false, true} {
		if _, err := r.SetCoordinator(spec, CoordinatorChange{SessionID: "local", Replace: replace}); !errors.As(err, &exists) || exists.Existing[0].HostID != hostID {
			t.Fatalf("replace=%v err=%v", replace, err)
		}
	}
	var taken CoordinatorNameTakenError
	other := CoordinatorSpec{ProjectID: "p-notes", ProjectRoot: "/work/notes", Name: "widget-lead", Surface: "claude", Cwd: "/work/notes"}
	if _, err := r.SetCoordinator(other, CoordinatorChange{}); !errors.As(err, &taken) || taken.HostID != hostID {
		t.Fatalf("name err=%v", err)
	}
	if mirrored, err := r.ProjectCoordinators(remote.ProjectID); err != nil || len(mirrored) != 1 || mirrored[0].SessionID != "thread-1" || !mirrored[0].ChangedAt.Equal(remote.ChangedAt) {
		t.Fatalf("mirrored=%+v err=%v", mirrored, err)
	}
	if _, err := r.RemoveHost(hostID); err != nil {
		t.Fatal(err)
	}
	if left, err := r.Coordinators(); err != nil || len(left) != 0 {
		t.Fatalf("left=%+v err=%v", left, err)
	}
	if _, err := r.SetCoordinator(spec, CoordinatorChange{SessionID: "local"}); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorLockPathIsOneFileForAnyProjectID(t *testing.T) {
	r := coordinatorRegistry(t)
	path := r.CoordinatorLockPath("repo:github.com/example/widget::apps/web")
	if filepath.Dir(path) != filepath.Dir(r.path) || strings.ContainsAny(filepath.Base(path), "/:") {
		t.Fatalf("path=%s", path)
	}
	if path == r.CoordinatorLockPath("repo:github.com/example/widget") {
		t.Fatal("two projects share a lock")
	}
}

func TestACoordinatorsNameMovesOnlyThroughTheCoordinator(t *testing.T) {
	r := coordinatorRegistry(t, "lead-session", "other")
	started := CoordinatorSpec{ProjectID: "p-started", ProjectRoot: "/work/a", Name: "a-lead", Surface: "claude", Cwd: "/work/a"}
	unstarted := CoordinatorSpec{ProjectID: "p-unstarted", ProjectRoot: "/work/b", Name: "b-lead", Surface: "claude", Cwd: "/work/b"}
	if _, err := r.SetCoordinator(started, CoordinatorChange{SessionID: "lead-session"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetCoordinator(unstarted, CoordinatorChange{}); err != nil {
		t.Fatal(err)
	}
	var aliasErr CoordinatorAliasError
	for name, attempt := range map[string]func() error{
		"rename the coordinator's session": func() error { return r.SetAlias("renamed", "lead-session") },
		"give its name to another session": func() error { return r.SetAlias("a-lead", "other") },
		"take an unstarted coordinator's name": func() error { return r.SetAlias("b-lead", "other") },
		"remove the name": func() error { return r.RemoveAlias("a-lead") },
	} {
		if err := attempt(); !errors.As(err, &aliasErr) {
			t.Fatalf("%s: err=%v", name, err)
		}
	}
	if generated, err := r.ReserveGeneratedAlias("other", "b-lead"); err != nil || generated == "b-lead" {
		t.Fatalf("generated=%q err=%v", generated, err)
	}
	if spec, _ := r.Coordinator("p-started"); spec.SessionID != "lead-session" {
		t.Fatalf("coordinator=%+v", spec)
	}
	if _, err := r.ClearCoordinator("p-started", "fixture", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := r.SetAlias("renamed", "lead-session"); err != nil {
		t.Fatalf("a former coordinator's session cannot be renamed: %v", err)
	}
}
