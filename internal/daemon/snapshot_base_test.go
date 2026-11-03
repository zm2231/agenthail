package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/deliverypolicy"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func baseFixture(t *testing.T) (*Daemon, *registry.Registry, *daemonSurface, *dashboardServer, http.Handler, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	d, reg, fake, _, _ := daemonFixture(t)
	cwd := catalogTestRepo(t)
	for _, id := range []string{"from", "to"} {
		session := fake.sessions[id]
		session.Cwd = cwd
		session.LastActive = time.Now().Add(-time.Minute)
		fake.sessions[id] = session
		if err := reg.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	d.discoverCatalog(context.Background())
	dashboard := &dashboardServer{token: "secret"}
	return d, reg, fake, dashboard, d.dashboardHandler(dashboard), cwd
}

func TestTheBaseSnapshotCarriesNoSessionStateAndItsTagIgnoresSessionActivity(t *testing.T) {
	d, reg, fake, dashboard, handler, cwd := baseFixture(t)
	first := getSnapshot(t, handler, "/api/v1/snapshot?sessions=none", "")
	tag := first.Header().Get("ETag")
	if first.Code != http.StatusOK || tag == "" {
		t.Fatalf("status=%d etag=%q", first.Code, tag)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(first.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"sessions", "totalSessions", "counts", "history", "nextCursor"} {
		if _, ok := body[absent]; ok {
			t.Fatalf("the base carries %q", absent)
		}
	}
	var projects []map[string]json.RawMessage
	if err := json.Unmarshal(body["projects"], &projects); err != nil || len(projects) == 0 {
		t.Fatalf("projects=%s err=%v", body["projects"], err)
	}
	for _, project := range projects {
		if _, ok := project["lastActive"]; ok {
			t.Fatalf("a base project carries activity: %v", project)
		}
		if _, ok := project["sessionCount"]; ok {
			t.Fatalf("a base project carries a session count: %v", project)
		}
	}

	busy := fake.sessions["from"]
	busy.Status = surface.StatusBusy
	busy.LastActive = time.Now()
	fake.sessions["from"] = busy
	d.discoverCatalog(context.Background())
	d.publishEvent("session.updated", "from", nil)
	d.publishEvent("turn.completed", "from", nil)
	if held := getSnapshot(t, handler, "/api/v1/snapshot?sessions=none", tag); held.Code != http.StatusNotModified {
		t.Fatalf("session activity moved the base tag: status=%d etag=%q want %q", held.Code, held.Header().Get("ETag"), tag)
	}

	if _, err := reg.SetCoordinator(registry.CoordinatorSpec{ProjectID: projectIDFor(t, body), ProjectRoot: cwd, Name: "base-lead", Surface: string(surface.KindCodex), Cwd: cwd}, registry.CoordinatorChange{SessionID: "to"}); err != nil {
		t.Fatal(err)
	}
	dashboard.invalidate()
	led := getSnapshot(t, handler, "/api/v1/snapshot?sessions=none", tag)
	if led.Code != http.StatusOK || led.Header().Get("ETag") == tag {
		t.Fatalf("a new lead kept the base tag: status=%d", led.Code)
	}
	tag = led.Header().Get("ETag")

	config, err := LoadDashboardConfig()
	if err != nil {
		t.Fatal(err)
	}
	config.BusyDelivery = deliverypolicy.Steer
	if err := SaveDashboardConfig(config); err != nil {
		t.Fatal(err)
	}
	d.publishEvent("settings.updated", "", nil)
	if changed := getSnapshot(t, handler, "/api/v1/snapshot?sessions=none", tag); changed.Code != http.StatusOK || changed.Header().Get("ETag") == tag {
		t.Fatalf("a settings change kept the base tag: status=%d", changed.Code)
	}
}

func projectIDFor(t *testing.T, base map[string]json.RawMessage) string {
	t.Helper()
	var projects []struct {
		Checkouts []struct {
			ProjectID string `json:"projectId"`
		} `json:"checkouts"`
	}
	if err := json.Unmarshal(base["projects"], &projects); err != nil || len(projects) == 0 || len(projects[0].Checkouts) == 0 {
		t.Fatalf("no checkout in %s", base["projects"])
	}
	return projects[0].Checkouts[0].ProjectID
}

func TestProjectActivityComesFromTheDaemonWithItsOwnTag(t *testing.T) {
	d, _, fake, _, handler, _ := baseFixture(t)
	first := getSnapshot(t, handler, "/api/v1/projects/activity", "")
	tag := first.Header().Get("ETag")
	var page projectActivityPage
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil || first.Code != http.StatusOK || tag == "" {
		t.Fatalf("status=%d etag=%q err=%v", first.Code, tag, err)
	}
	if len(page.Projects) != 1 || page.Projects[0].SessionCount != 2 || len(page.Projects[0].Checkouts) != 1 || page.Projects[0].Checkouts[0].SessionCount != 2 || page.Projects[0].LastActive.IsZero() {
		t.Fatalf("activity=%+v", page.Projects)
	}
	if held := getSnapshot(t, handler, "/api/v1/projects/activity", tag); held.Code != http.StatusNotModified {
		t.Fatalf("unchanged activity status=%d", held.Code)
	}
	later := fake.sessions["from"]
	later.LastActive = time.Now().Add(time.Second)
	fake.sessions["from"] = later
	d.discoverCatalog(context.Background())
	if moved := getSnapshot(t, handler, "/api/v1/projects/activity", tag); moved.Code != http.StatusOK || moved.Header().Get("ETag") == tag {
		t.Fatalf("new activity kept the tag: status=%d", moved.Code)
	}
}
