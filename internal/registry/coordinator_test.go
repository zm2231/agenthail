package registry

import (
	"path/filepath"
	"testing"

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
	if err := r.SetCoordinator(beta, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.SetCoordinator(alpha, "old"); err != nil {
		t.Fatal(err)
	}
	if err := r.HandOverCoordinator(alpha, "old", "new"); err != nil {
		t.Fatal(err)
	}
	if err := r.SetCoordinator(beta, "old"); err != nil {
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
	if err := r.SetCoordinator(spec, "previous"); err != nil {
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
