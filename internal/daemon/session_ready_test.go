package daemon

import (
	"net/http"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestEveryWayToStartASessionRecordsItBeforeItsFirstTurn(t *testing.T) {
	for _, tc := range []struct {
		name   string
		launch bool
		via    registry.OriginVia
	}{
		{name: "session-create", via: registry.OriginPerson},
		{name: "session-create through the codex-app-server launcher", launch: true, via: registry.OriginPerson},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, r, fake, _, _ := daemonFixture(t)
			if tc.launch {
				d.SetLaunchers(surface.NewLaunchers([]surface.Surface{fake}))
			}
			var seen string
			fake.firstTurn = func() { seen = recordedBeforeFirstTurn(r, "started", "starter") }
			body := `{"action":"session-create","surface":"codex","message":"hello","alias":"starter"}`
			if tc.launch {
				body = `{"action":"session-create","surface":"codex","launcher":"codex-app-server","message":"hello","alias":"starter"}`
			}
			if w := serveDashboardRequest(dashboardRouter(d), http.MethodPost, "/api/action", body); w.Code != http.StatusCreated {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if seen != string(tc.via) {
				t.Fatalf("at the first turn: %s", seen)
			}
		})
	}

	t.Run("a new coordinator", func(t *testing.T) {
		d, r, fake, repo := projectsFixture(t)
		if response := runDashboardAction(t, d, `{"action":"project-add","path":"`+repo+`"}`); response.Code != http.StatusOK {
			t.Fatalf("add: %d %s", response.Code, response.Body)
		}
		var seen string
		fake.firstTurn = func() { seen = recordedBeforeFirstTurn(r, "started", "") }
		if response := runDashboardAction(t, d, `{"action":"coordinator-new","surface":"codex","cwd":"`+repo+`","name":"captain"}`); response.Code != http.StatusOK {
			t.Fatalf("coordinator-new: %d %s", response.Code, response.Body)
		}
		if response := runDashboardAction(t, d, `{"action":"coordinator-send","name":"captain","message":"hello"}`); response.Code != http.StatusOK {
			t.Fatalf("coordinator-send: %d %s", response.Code, response.Body)
		}
		if seen != string(registry.OriginCoordinator) {
			t.Fatalf("at the first turn: %s", seen)
		}
	})
}

func recordedBeforeFirstTurn(r *registry.Registry, id, alias string) string {
	if _, err := r.Session(id); err != nil {
		return "not registered: " + err.Error()
	}
	if alias != "" {
		if bound, err := r.ReverseAlias(id); err != nil || bound != alias {
			return "alias " + bound + " not bound"
		}
	}
	origin, found, err := r.SessionOrigin(id)
	if err != nil || !found {
		return "no origin"
	}
	return string(origin.Via)
}

func TestASessionThatCannotBeRecordedBeforeItsFirstTurnIsAStorageFailureNotAFailedDelivery(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "session-create", body: `{"action":"session-create","surface":"codex","message":"hello","alias":"starter"}`},
		{name: "through a starter launcher", body: `{"action":"session-create","surface":"codex","launcher":"codex-app-server","message":"hello","alias":"starter"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, r, fake, _, _ := daemonFixture(t)
			d.SetLaunchers(surface.NewLaunchers([]surface.Surface{fake}))
			turned := false
			fake.firstTurn = func() { turned = true }
			fake.unrecordable = true
			w := serveDashboardRequest(dashboardRouter(d), http.MethodPost, "/api/action", tc.body)
			if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "create_storage_failed") || !strings.Contains(w.Body.String(), "which was not sent") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if turned {
				t.Fatal("the first turn ran though the session was not recorded")
			}
			history, err := r.ListHistory(10, "")
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range history {
				if entry.SessionID == "started" && (entry.Kind == "failed" || entry.Kind == "sent" || entry.Kind == "submitted") {
					t.Fatalf("a delivery was recorded for a turn that never ran: %+v", entry)
				}
			}
		})
	}

	t.Run("a new coordinator", func(t *testing.T) {
		d, _, fake, repo := projectsFixture(t)
		if response := runDashboardAction(t, d, `{"action":"project-add","path":"`+repo+`"}`); response.Code != http.StatusOK {
			t.Fatalf("add: %d %s", response.Code, response.Body)
		}
		if response := runDashboardAction(t, d, `{"action":"coordinator-new","surface":"codex","cwd":"`+repo+`","name":"captain"}`); response.Code != http.StatusOK {
			t.Fatalf("coordinator-new: %d %s", response.Code, response.Body)
		}
		turned := false
		fake.firstTurn = func() { turned = true }
		fake.unrecordable = true
		response := runDashboardAction(t, d, `{"action":"coordinator-send","name":"captain","message":"hello","author":"person"}`)
		if response.Code < 400 || !strings.Contains(response.Body.String(), "which was not sent") {
			t.Fatalf("coordinator-send: %d %s", response.Code, response.Body)
		}
		if turned {
			t.Fatal("the first turn ran though the session was not recorded")
		}
		d.delivered.mu.Lock()
		held := len(d.delivered.bySession["started"]) + len(d.delivered.bySession[startingSession])
		d.delivered.mu.Unlock()
		if held != 0 {
			t.Fatalf("an unsent first turn is still held as delivered: %d entries", held)
		}
	})
}
