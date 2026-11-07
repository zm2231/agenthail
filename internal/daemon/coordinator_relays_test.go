package daemon

import (
	"net/http"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
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
