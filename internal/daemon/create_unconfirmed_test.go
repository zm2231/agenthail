package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/claudesurface"
)

func TestACreateWhoseOpenIsNotConfirmedLeavesNoSessionAndFreesItsName(t *testing.T) {
	r, fake, session, args := taskCreator(t)
	delete(args, "task")
	args["alias"] = "sideprobe"
	fake.openUnconfirmed = surface.DeliveryOutcomeUnknown(errors.New("the Claude host did not answer"))
	first, refused := callMCP(t, session, "agenthail_create", args)
	if !refused {
		t.Fatalf("first=%v, want the create refused", first)
	}
	if _, err := r.Session("started"); err == nil {
		t.Fatal("the unconfirmed session was recorded")
	}
	if owner, err := r.LookupAlias("sideprobe"); err == nil {
		t.Fatalf("@sideprobe is held by %s after a create that opened nothing", owner)
	}
	again, refused := callMCP(t, session, "agenthail_create", args)
	if !refused || again["code"] != first["code"] || again["message"] != first["message"] {
		t.Fatalf("again=%v first=%v, want the first outcome replayed", again, first)
	}
	if len(fake.startOptions) != 1 {
		t.Fatalf("starts=%d, want the replay to start nothing", len(fake.startOptions))
	}
	fake.openUnconfirmed = nil
	args["clientRequestId"] = "c2"
	if out, refused := callMCP(t, session, "agenthail_create", args); refused {
		t.Fatalf("a new create under the same name: %v", out)
	}
	if owner, err := r.LookupAlias("sideprobe"); err != nil || owner != "started" {
		t.Fatalf("@sideprobe -> %q err=%v, want the session that opened", owner, err)
	}
}

func TestACreateWhoseFirstTurnIsUnknownNamesItsSessionAndAReplayReturnsIt(t *testing.T) {
	_, fake, session, args := taskCreator(t)
	delete(args, "task")
	fake.startErr = surface.DeliveryOutcomeUnknown(errors.New("turn outcome unknown"))
	first, refused := callMCP(t, session, "agenthail_create", args)
	if refused || first["sessionId"] != "started" || first["outcome"] != "unknown" {
		t.Fatalf("first=%v, want the opened session named with its first turn's outcome unknown", first)
	}
	again, refused := callMCP(t, session, "agenthail_create", args)
	if refused || again["sessionId"] != first["sessionId"] || again["outcome"] != "unknown" {
		t.Fatalf("again=%v first=%v", again, first)
	}
	if len(fake.startOptions) != 1 {
		t.Fatalf("starts=%d, want the replay to start nothing", len(fake.startOptions))
	}
}

func TestArchivingASessionThatNeverOpenedListsItAsArchived(t *testing.T) {
	fixture := claudehosttest.New(t, "")
	fixture.Start(t)
	claude := claudesurface.NewClaude(nil, fixture.Home)
	r := hostRegistry(t)
	d := newTestDaemon(t, r, []surface.Surface{claude})
	id := uuid.NewString()
	reservation, err := r.ReserveAlias("sideprobe", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RecordStartingSession(surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Transport: "uds", Cwd: fixture.Cwd, Status: surface.StatusIdle, Source: "agenthail", LastActive: time.Now()}, reservation, registry.OriginOf(id, "")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RecordSessionCreationIntent(id, surface.PersonSessionID, uuid.NewString(), "probe"); err != nil {
		t.Fatal(err)
	}
	d.discoverCatalog(context.Background())
	if response := runDashboardAction(t, d, `{"action":"session-archive","sessionId":"`+id+`"}`); response.Code != http.StatusOK {
		t.Fatalf("archive: %d %s", response.Code, response.Body)
	}
	for range 3 {
		d.discoverCatalog(context.Background())
	}
	state, err := d.dashboardState(context.Background(), registry.CatalogPageRequest{Scope: "archived", Limit: 50, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if row := stateRow(t, state, id); !row.Archived {
		t.Fatalf("archived listing has %+v, want it archived", row)
	}
	response := httptest.NewRecorder()
	d.dashboardSessionHandler(response, httptest.NewRequest(http.MethodGet, "/api/v1/session?id="+id, nil))
	var read struct {
		Archived *bool `json:"archived"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &read); err != nil || read.Archived == nil || !*read.Archived {
		t.Fatalf("session read %d %s, want archived true", response.Code, response.Body)
	}
}

func TestAStartThroughALauncherWhoseOpenIsNotConfirmedLeavesNoSessionAndFreesItsName(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	d.SetLaunchers(surface.NewLaunchers([]surface.Surface{fake}))
	fake.openUnconfirmed = surface.DeliveryOutcomeUnknown(errors.New("the provider did not answer"))
	body := `{"action":"session-create","surface":"codex","launcher":"codex-app-server","message":"hello","alias":"starter"}`
	if w := serveDashboardRequest(dashboardRouter(d), http.MethodPost, "/api/action", body); w.Code < http.StatusMultipleChoices {
		t.Fatalf("status=%d body=%s, want the start refused", w.Code, w.Body.String())
	}
	if _, err := r.Session("started"); err == nil {
		t.Fatal("the unconfirmed session was recorded")
	}
	if owner, err := r.LookupAlias("starter"); err == nil {
		t.Fatalf("@starter is held by %s after a start that opened nothing", owner)
	}
	fake.openUnconfirmed = nil
	if w := serveDashboardRequest(dashboardRouter(d), http.MethodPost, "/api/action", body); w.Code != http.StatusCreated {
		t.Fatalf("a new start under the same name: %d %s", w.Code, w.Body.String())
	}
}
