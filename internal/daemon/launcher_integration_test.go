package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

type launcherFixture struct {
	id        string
	agents    []surface.SurfaceKind
	result    surface.LaunchResult
	locations map[string]surface.Location
	launches  int
}

func (l *launcherFixture) ID() string                               { return l.id }
func (l *launcherFixture) Agents() []surface.SurfaceKind            { return l.agents }
func (l *launcherFixture) Available(context.Context) (bool, string) { return true, "fixture" }
func (l *launcherFixture) Launch(context.Context, surface.LaunchRequest) (surface.LaunchResult, error) {
	l.launches++
	return l.result, nil
}
func (l *launcherFixture) Locate(context.Context, []surface.Session) map[string]surface.Location {
	return l.locations
}

func TestSessionCreateKnownLauncherIDPreservesProviderSession(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	fake.kind = surface.KindClaude
	d.Surfaces = []surface.Surface{fake}
	launcher := &launcherFixture{id: surface.LauncherClaudeBG, agents: []surface.SurfaceKind{surface.KindClaude}, result: surface.LaunchResult{SessionID: "created", Session: &surface.Session{ID: "created", Surface: surface.KindClaude, Name: "Created", Cwd: "/repo", Status: surface.StatusIdle}}}
	d.SetLaunchers([]surface.Launcher{launcher})
	w := httptest.NewRecorder()
	d.dashboardActionHandler(w, httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(`{"action":"session-create","surface":"claude","launcher":"claude-bg","message":"hello"}`)))
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	created, err := r.Session("created")
	if err != nil {
		t.Fatal(err)
	}
	if created.Surface != surface.KindClaude || created.Cwd != "/repo" || created.Name != "Created" {
		t.Fatalf("created=%+v", created)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["sessionId"] != "created" {
		t.Fatalf("body=%s", w.Body.String())
	}
}

func TestSessionCreateTerminalLocationPersistsPendingWithoutGuessingID(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	fake.kind = surface.KindClaude
	d.Surfaces = []surface.Surface{fake}
	location := &surface.Location{Workspace: "workspace-1", Surface: "surface-1"}
	launcher := &launcherFixture{id: surface.LauncherCMUX, agents: []surface.SurfaceKind{surface.KindClaude}, result: surface.LaunchResult{Location: location}}
	d.SetLaunchers([]surface.Launcher{launcher})
	w := httptest.NewRecorder()
	d.dashboardActionHandler(w, httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(`{"action":"session-create","surface":"claude","launcher":"cmux","message":"hello"}`)))
	if w.Code != http.StatusCreated || strings.Contains(w.Body.String(), `"sessionId"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	pending, err := r.PendingLaunches()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Location.Workspace != location.Workspace {
		t.Fatalf("pending=%+v", pending)
	}
}

func TestSessionCreateRejectsUnsupportedAdvancedOptionsBeforeLaunch(t *testing.T) {
	d, _, fake, _, _ := daemonFixture(t)
	fake.kind = surface.KindClaude
	d.Surfaces = []surface.Surface{fake}
	launcher := &launcherFixture{id: surface.LauncherClaudeBG, agents: []surface.SurfaceKind{surface.KindClaude}}
	d.SetLaunchers([]surface.Launcher{launcher})
	w := httptest.NewRecorder()
	d.dashboardActionHandler(w, httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(`{"action":"session-create","surface":"claude","launcher":"claude-bg","message":"hello","worktree":"/tmp/w"}`)))
	if w.Code != http.StatusBadRequest || launcher.launches != 0 {
		t.Fatalf("status=%d launches=%d body=%s", w.Code, launcher.launches, w.Body.String())
	}
}

func TestLocateLaunchedSessionRejectsAmbiguousLocation(t *testing.T) {
	fake := &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{"a": {ID: "a"}, "b": {ID: "b"}}}
	launcher := &launcherFixture{id: surface.LauncherCMUX, locations: map[string]surface.Location{"a": {Workspace: "w", Surface: "s"}, "b": {Workspace: "w", Surface: "s"}}}
	_, _, err := locateLaunchedSession(context.Background(), launcher, fake, surface.LaunchResult{Location: &surface.Location{Workspace: "w", Surface: "s"}})
	if err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("err=%v", err)
	}
}
