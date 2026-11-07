package daemon

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestALeadReplacedByAnotherSessionHandsItsRelaysToTheNewLead(t *testing.T) {
	d, reg, _, _, _, cwd := baseFixture(t)
	for _, id := range []string{"worker", "reviewer"} {
		if err := reg.RegisterSession(surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Cwd: cwd}); err != nil {
			t.Fatal(err)
		}
	}
	handler := d.dashboardHandler(&dashboardServer{token: dashboardTestToken})
	if set := postAction(t, handler, map[string]any{"action": "coordinator-set", "sessionId": "to", "name": "lead-a"}); set.Code != http.StatusOK {
		t.Fatalf("set: %d %s", set.Code, set.Body.String())
	}
	for _, route := range [][2]string{{"worker", "to"}, {"to", "reviewer"}} {
		if _, err := reg.AddRoute(route[0], route[1], ".*"); err != nil {
			t.Fatal(err)
		}
	}
	if replaced := postAction(t, handler, map[string]any{"action": "coordinator-set", "sessionId": "from", "name": "lead-a", "replace": true}); replaced.Code != http.StatusOK {
		t.Fatalf("replace: %d %s", replaced.Code, replaced.Body.String())
	}
	routes, err := reg.ListRoutes()
	if err != nil {
		t.Fatal(err)
	}
	got := map[[2]string]bool{}
	for _, route := range routes {
		got[[2]string{route.FromSession, route.ToSession}] = true
	}
	if len(routes) != 2 || !got[[2]string{"worker", "from"}] || !got[[2]string{"from", "reviewer"}] {
		t.Fatalf("after the replace the relays are %+v, want them on the new lead", routes)
	}
}

func TestALeadReplacedFromAPairedHostHandsItsRelaysToTheNewLeadOnBothHosts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	upstream := catalogTestRepo(t)
	laptop := newFederationNode(t, "laptop-a", "laptop-b")
	vps := newFederationNode(t, "vps-a", "vps-b")
	moveSessions(t, laptop, filepath.Join(cloneAt(t, upstream, filepath.Join(t.TempDir(), "laptop-gadget")), "src"))
	moveSessions(t, vps, filepath.Join(cloneAt(t, upstream, filepath.Join(t.TempDir(), "vps-gadget")), "src"))
	laptop.serve(t, nil)
	vps.serve(t, nil)
	roots := testRoots(laptop.server)
	laptop.start(t, roots)
	vps.start(t, roots)
	vpsRecord := laptop.pair(t, vps, "vps")
	laptopRecord := vps.pair(t, laptop, "laptop")
	testkit.Eventually(t, "both mirrors", 10*time.Second, func() bool {
		return len(sessionsOf(laptop.snapshot(t), vpsRecord.ID)) == 2 && len(sessionsOf(vps.snapshot(t), laptopRecord.ID)) == 2
	})
	const project = "repo:github.com/example/gadget"
	if set := vps.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": "vps-a", "name": "gadget-lead"}); set.Code != http.StatusOK {
		t.Fatalf("set on vps: %d %s", set.Code, set.Body)
	}
	testkit.Eventually(t, "laptop mirrors vps's lead", 10*time.Second, func() bool {
		mirrored, _ := laptop.reg.ProjectCoordinators(project)
		return len(mirrored) == 1 && mirrored[0].HostID == vpsRecord.ID
	})
	if _, err := vps.reg.AddRoute("vps-b", "vps-a", ".*"); err != nil {
		t.Fatal(err)
	}
	if _, err := laptop.reg.AddRoute("laptop-b", "vps-a", ".*"); err != nil {
		t.Fatal(err)
	}
	if replaced := laptop.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": "laptop-a", "name": "laptop-lead", "replace": true}); replaced.Code != http.StatusOK {
		t.Fatalf("replace from laptop: %d %s", replaced.Code, replaced.Body)
	}
	for name, node := range map[string]*federationNode{"vps": vps, "laptop": laptop} {
		from := name + "-b"
		routes, err := node.reg.ListRoutes()
		if err != nil {
			t.Fatal(err)
		}
		if len(routes) != 1 || routes[0].FromSession != from || routes[0].ToSession != "laptop-a" {
			t.Fatalf("%s relays after the replace: %+v, want %s to the new lead laptop-a", name, routes, from)
		}
	}
}
