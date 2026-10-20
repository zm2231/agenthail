package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestANameIsHeldWhileItsConversationStarts(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	router := dashboardRouter(d)
	fake.startHook = func() {
		rename := serveDashboardRequest(router, http.MethodPost, "/api/action", `{"action":"alias","sessionId":"from","alias":"builder"}`)
		if rename.Code != http.StatusConflict || !strings.Contains(rename.Body.String(), "@builder is the name of a conversation being started") {
			t.Errorf("rename during the start: %d %s", rename.Code, rename.Body)
		}
		second := serveDashboardRequest(router, http.MethodPost, "/api/action", `{"action":"session-create","surface":"codex","message":"again","alias":"builder"}`)
		if second.Code != http.StatusBadRequest || len(fake.startOptions) != 1 {
			t.Errorf("second start with the same name: %d starts=%d %s", second.Code, len(fake.startOptions), second.Body)
		}
		var reserved registry.AliasReservedError
		if _, err := r.SetCoordinator(registry.CoordinatorSpec{ProjectID: "project-fixture", ProjectRoot: "/repo", Name: "builder", Surface: "codex", Cwd: "/repo"}, registry.CoordinatorChange{}); !errors.As(err, &reserved) {
			t.Errorf("coordinator took the name during the start: err=%v", err)
		}
	}
	created := serveDashboardRequest(router, http.MethodPost, "/api/action", `{"action":"session-create","surface":"codex","message":"hello","alias":"builder"}`)
	if created.Code != http.StatusOK && created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body)
	}
	if owner, err := r.LookupAlias("builder"); err != nil || owner != "started" {
		t.Fatalf("owner=%q err=%v", owner, err)
	}
}

func TestAStartThatFailsFreesItsName(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	fake.kind = surface.KindClaude
	d.Surfaces = []surface.Surface{fake}
	d.SetLaunchers([]surface.Launcher{&launcherFixture{id: surface.LauncherCMUX, agents: []surface.SurfaceKind{surface.KindClaude}, launchErr: errors.New("cmux is not running")}})
	w := serveDashboardRequest(dashboardRouter(d), http.MethodPost, "/api/action", fmt.Sprintf(`{"action":"session-create","surface":"claude","launcher":"cmux","message":"hello","cwd":%q,"alias":"builder"}`, t.TempDir()))
	if w.Code < 400 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	if err := r.SetAlias("builder", "from"); err != nil {
		t.Fatalf("the failed start kept its name: %v", err)
	}
}

func TestAPendingLaunchIsForgottenOnlyAfterItsRetention(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	fake.kind = surface.KindClaude
	d.Surfaces = []surface.Surface{fake}
	d.SetLaunchers([]surface.Launcher{&launcherFixture{id: surface.LauncherCMUX, agents: []surface.SurfaceKind{surface.KindClaude}}})
	record := func(name string, launchedAt time.Time) {
		t.Helper()
		reservation, err := r.ReserveAlias(name, time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.RecordPendingLaunch(surface.LauncherCMUX, surface.KindClaude, "/repo", "", reservation, surface.Location{Workspace: name}, launchedAt, launchedAt.Add(pendingLaunchLifetime)); err != nil {
			t.Fatal(err)
		}
	}
	record("lapsed", time.Now().Add(-time.Hour))
	record("forgotten", time.Now().Add(-pendingLaunchLifetime-pendingLaunchRetention-time.Minute))
	d.correlatePendingLaunches(context.Background(), nil)
	pending, err := r.PendingLaunches()
	if err != nil || len(pending) != 1 || pending[0].Alias.Name != "lapsed" {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	if err := r.SetAlias("lapsed", "from"); err != nil {
		t.Fatalf("a lapsed launch still holds its name: %v", err)
	}
}

func TestAForkIsNamedOnTheHostThatForksIt(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	adapter := &operationSurface{daemonSurface: fake}
	d.Surfaces = []surface.Surface{adapter}
	router := dashboardRouter(d)
	if _, err := r.ReserveAlias("held", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	refused := serveDashboardRequest(router, http.MethodPost, "/api/action", `{"action":"session-fork","sessionId":"from","alias":"held"}`)
	if refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), "@held is the name of a conversation being started") {
		t.Fatalf("fork with a held name: %d %s", refused.Code, refused.Body)
	}
	if _, err := r.Session("forked"); err == nil {
		t.Fatal("a fork refused its name was still made")
	}
	if forked := serveDashboardRequest(router, http.MethodPost, "/api/action", `{"action":"session-fork","sessionId":"from","alias":"branch"}`); forked.Code != http.StatusOK {
		t.Fatalf("fork: %d %s", forked.Code, forked.Body)
	}
	if owner, err := r.LookupAlias("branch"); err != nil || owner != "forked" {
		t.Fatalf("owner=%q err=%v", owner, err)
	}
}
