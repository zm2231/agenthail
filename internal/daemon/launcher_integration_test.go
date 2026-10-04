package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

type launcherFixture struct {
	id               string
	agents           []surface.SurfaceKind
	result           surface.LaunchResult
	locations        map[string]surface.Location
	launches         int
	locateContextErr error
	launchErr        error
}

func (l *launcherFixture) ID() string                               { return l.id }
func (l *launcherFixture) Agents() []surface.SurfaceKind            { return l.agents }
func (l *launcherFixture) Available(context.Context) (bool, string) { return true, "fixture" }
func (l *launcherFixture) Launch(context.Context, surface.LaunchRequest) (surface.LaunchResult, error) {
	l.launches++
	if l.launchErr != nil {
		return surface.LaunchResult{}, l.launchErr
	}
	return l.result, nil
}

func (l *launcherFixture) Locate(ctx context.Context, _ []surface.Session) map[string]surface.Location {
	l.locateContextErr = ctx.Err()
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

func TestSessionCreateStarterLauncherKeepsCreatedSessionWhenTurnOutcomeIsUnknown(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	fake.startErr = surface.DeliveryOutcomeUnknown(errors.New("turn/start timed out"))
	d.SetLaunchers(surface.NewLaunchers([]surface.Surface{fake}))
	w := httptest.NewRecorder()
	d.dashboardActionHandler(w, httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(`{"action":"session-create","surface":"codex","launcher":"codex-app-server","message":"hello","alias":"starter"}`)))
	if w.Code != http.StatusAccepted || len(fake.startOptions) != 1 {
		t.Fatalf("status=%d starts=%d body=%s", w.Code, len(fake.startOptions), w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true || body["accepted"] != true || body["retryable"] != false || body["sessionId"] != "started" || body["warning"] == nil {
		t.Fatalf("body=%s", w.Body.String())
	}
	created, err := r.Session("started")
	if err != nil || created.Runtime == nil || created.Runtime.Launcher != surface.LauncherCodexAppServer {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	if alias, err := r.ReverseAlias("started"); err != nil || alias != "starter" {
		t.Fatalf("alias=%q err=%v", alias, err)
	}
}

func TestSessionCreateTerminalLocationPersistsPendingWithoutGuessingID(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	fake.kind = surface.KindClaude
	d.Surfaces = []surface.Surface{fake}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	location := &surface.Location{Workspace: "workspace-1", Surface: "surface-1"}
	launcher := &launcherFixture{id: surface.LauncherCMUX, agents: []surface.SurfaceKind{surface.KindClaude}, result: surface.LaunchResult{Location: location}}
	d.SetLaunchers([]surface.Launcher{launcher})
	w := httptest.NewRecorder()
	d.dashboardActionHandler(w, httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(fmt.Sprintf(`{"action":"session-create","surface":"claude","launcher":"cmux","message":"hello","cwd":%q,"alias":"builder"}`, cwd))))
	if w.Code != http.StatusCreated || strings.Contains(w.Body.String(), `"sessionId"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	pending, err := r.PendingLaunches()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Location.Workspace != location.Workspace || pending[0].Alias != "builder" {
		t.Fatalf("pending=%+v", pending)
	}
	launcher.locations = map[string]surface.Location{"created": {Workspace: "workspace-1", Surface: "surface-1"}}
	d.correlatePendingLaunches(context.Background(), []surface.Session{{ID: "created", Surface: surface.KindClaude, Cwd: cwd, Status: surface.StatusIdle}})
	alias, err := r.ReverseAlias("created")
	if err != nil || alias != "builder" {
		t.Fatalf("alias=%q err=%v", alias, err)
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

func TestSessionCreateReportsAcceptedUnresolvedLaunchWithoutRetrySignal(t *testing.T) {
	d, _, fake, _, _ := daemonFixture(t)
	fake.kind = surface.KindClaude
	d.Surfaces = []surface.Surface{fake}
	launcher := &launcherFixture{id: surface.LauncherCMUX, agents: []surface.SurfaceKind{surface.KindClaude}, launchErr: surface.LaunchAcceptedError{Launcher: surface.LauncherCMUX, Err: errors.New("malformed output")}}
	d.SetLaunchers([]surface.Launcher{launcher})
	w := httptest.NewRecorder()
	d.dashboardActionHandler(w, httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(`{"action":"session-create","surface":"claude","launcher":"cmux","message":"hello"}`)))
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"ok":true`) || !strings.Contains(w.Body.String(), `"status":"submitted"`) || !strings.Contains(w.Body.String(), `"accepted":true`) || !strings.Contains(w.Body.String(), `"retryable":false`) || !strings.Contains(w.Body.String(), `"warning":`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestSessionCreateReportsAcceptedLaunchWhenDiscoveryFails(t *testing.T) {
	d, _, fake, _, _ := daemonFixture(t)
	fake.kind = surface.KindClaude
	d.Surfaces = []surface.Surface{&failingClaudeListSurface{daemonSurface: fake}}
	launcher := &launcherFixture{
		id:     surface.LauncherCMUX,
		agents: []surface.SurfaceKind{surface.KindClaude},
		result: surface.LaunchResult{Location: &surface.Location{Workspace: "workspace-1"}},
	}
	d.SetLaunchers([]surface.Launcher{launcher})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(`{"action":"session-create","surface":"claude","launcher":"cmux","message":"hello"}`))
	req.Header.Set("Origin", "http://example.test")
	req.Host = "example.test"
	req.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	d.dashboardHandler(&dashboardServer{token: "secret"}).ServeHTTP(w, req)
	if w.Code != http.StatusAccepted || launcher.launches != 1 {
		t.Fatalf("status=%d launches=%d body=%s", w.Code, launcher.launches, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true || body["status"] != "submitted" || body["accepted"] != true || body["retryable"] != false || body["warning"] == nil {
		t.Fatalf("body=%s", w.Body.String())
	}
}

func TestSessionCreateRejectsAliasCollisionBeforeLaunch(t *testing.T) {
	d, r, fake, from, _ := daemonFixture(t)
	fake.kind = surface.KindClaude
	d.Surfaces = []surface.Surface{fake}
	if err := r.SetAlias("builder", from.ID); err != nil {
		t.Fatal(err)
	}
	launcher := &launcherFixture{id: surface.LauncherCMUX, agents: []surface.SurfaceKind{surface.KindClaude}}
	d.SetLaunchers([]surface.Launcher{launcher})
	w := httptest.NewRecorder()
	d.dashboardActionHandler(w, httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(`{"action":"session-create","surface":"claude","launcher":"cmux","message":"hello","alias":"builder"}`)))
	if w.Code != http.StatusBadRequest || launcher.launches != 0 {
		t.Fatalf("status=%d launches=%d body=%s", w.Code, launcher.launches, w.Body.String())
	}
	owner, err := r.LookupAlias("builder")
	if err != nil || owner != from.ID {
		t.Fatalf("alias owner=%q err=%v", owner, err)
	}
}

func TestSessionCreateRejectsMissingLauncherSurfaceBeforeLaunch(t *testing.T) {
	d, _, fake, _, _ := daemonFixture(t)
	fake.kind = surface.KindCodex
	d.Surfaces = []surface.Surface{fake}
	launcher := &launcherFixture{id: surface.LauncherCMUX, agents: []surface.SurfaceKind{surface.KindClaude}}
	d.SetLaunchers([]surface.Launcher{launcher})
	w := httptest.NewRecorder()
	d.dashboardActionHandler(w, httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(`{"action":"session-create","surface":"claude","launcher":"cmux","message":"hello"}`)))
	if w.Code != http.StatusConflict || launcher.launches != 0 {
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

func TestLocateLaunchedSessionRejectsUndiscoveredID(t *testing.T) {
	fake := &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{"other": {ID: "other"}}}
	launcher := &launcherFixture{id: surface.LauncherCMUX, locations: map[string]surface.Location{"missing": {Workspace: "workspace-1"}}}
	_, _, err := locateLaunchedSession(context.Background(), launcher, fake, surface.LaunchResult{Location: &surface.Location{Workspace: "workspace-1"}})
	if err == nil || !strings.Contains(err.Error(), "did not discover session") {
		t.Fatalf("err=%v", err)
	}
}

func TestPendingCorrelationFiltersAgentAndCwd(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	launcher := &launcherFixture{id: surface.LauncherCMUX, agents: []surface.SurfaceKind{surface.KindClaude}, locations: map[string]surface.Location{
		"codex":  {Workspace: "workspace-1", Surface: "surface-1"},
		"claude": {Workspace: "workspace-1", Surface: "surface-1"},
	}}
	d.SetLaunchers([]surface.Launcher{launcher})
	if _, err := r.RecordPendingLaunch(surface.LauncherCMUX, surface.KindClaude, "/repo", "Created", "", surface.Location{Workspace: "workspace-1"}); err != nil {
		t.Fatal(err)
	}
	d.correlatePendingLaunches(context.Background(), []surface.Session{
		{ID: "codex", Surface: surface.KindCodex, Cwd: "/repo"},
		{ID: "claude", Surface: surface.KindClaude, Cwd: "/other"},
	})
	pending, err := r.PendingLaunches()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending=%+v", pending)
	}
}

func TestWorkspaceOnlyLaunchCorrelationUsesDiscoveredLocation(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	fake.kind = surface.KindClaude
	d.Surfaces = []surface.Surface{fake}
	location := surface.Location{Workspace: "workspace-1"}
	fullLocation := surface.Location{Workspace: "workspace-1", Surface: "surface-1"}
	launcher := &launcherFixture{id: surface.LauncherCMUX, agents: []surface.SurfaceKind{surface.KindClaude}, locations: map[string]surface.Location{"created": fullLocation}}
	d.SetLaunchers([]surface.Launcher{launcher})
	if _, err := r.RecordPendingLaunch(surface.LauncherCMUX, surface.KindClaude, "/repo", "Created", "", location); err != nil {
		t.Fatal(err)
	}
	d.correlatePendingLaunches(context.Background(), []surface.Session{{ID: "created", Surface: surface.KindClaude, Cwd: "/repo", Status: surface.StatusIdle}})
	pending, err := r.PendingLaunches()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending=%+v", pending)
	}
	created, err := r.Session("created")
	if err != nil {
		t.Fatal(err)
	}
	if created.Runtime == nil || created.Runtime.Location == nil || *created.Runtime.Location != fullLocation {
		t.Fatalf("created runtime=%+v", created.Runtime)
	}
}

func TestCatalogDiscoveryUsesFreshLocateContextAndPreservesTerminalRuntime(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	fake.kind = surface.KindClaude
	session := surface.Session{ID: "hand-started", Surface: surface.KindClaude, Cwd: "/repo", Status: surface.StatusIdle}
	fake.sessions = map[string]surface.Session{session.ID: session}
	d.Surfaces = []surface.Surface{fake}
	launcher := &launcherFixture{id: surface.LauncherCMUX, agents: []surface.SurfaceKind{surface.KindClaude}, locations: map[string]surface.Location{session.ID: {Workspace: "workspace-1", Surface: "surface-1"}}}
	d.SetLaunchers([]surface.Launcher{launcher})
	d.discoverCatalog(context.Background())
	if launcher.locateContextErr != nil {
		t.Fatalf("Locate context err=%v", launcher.locateContextErr)
	}
	want := &surface.Runtime{Launcher: surface.LauncherCMUX, Location: &surface.Location{Workspace: "workspace-1", Surface: "surface-1"}, Focusable: true}
	stored, err := r.Session(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Runtime == nil || stored.Runtime.Launcher != want.Launcher || stored.Runtime.Location == nil || *stored.Runtime.Location != *want.Location || stored.Runtime.Focusable != want.Focusable {
		t.Fatalf("first discovery runtime=%+v", stored.Runtime)
	}
	d.discoverCatalog(context.Background())
	stored, err = r.Session(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Runtime == nil || stored.Runtime.Launcher != want.Launcher || stored.Runtime.Location == nil || *stored.Runtime.Location != *want.Location || !stored.Runtime.Focusable {
		t.Fatalf("second discovery runtime=%+v", stored.Runtime)
	}
}
