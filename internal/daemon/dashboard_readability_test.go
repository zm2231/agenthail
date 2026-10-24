package daemon

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

type readabilityDashboard struct {
	URL    string `json:"url"`
	Cookie string `json:"cookie"`
	Token  string `json:"token"`
}

func TestDashboardTextIsReadableOverEveryBackground(t *testing.T) {
	if os.Getenv("AGENTHAIL_BROWSER_GUARD") == "" {
		t.Skip("set AGENTHAIL_BROWSER_GUARD=1 and AGENTHAIL_BROWSER_CACHE to run the browser guard")
	}
	t.Setenv("HOME", t.TempDir())
	themes := map[string]map[string]readabilityDashboard{}
	for _, theme := range []string{"dark", "light"} {
		themes[theme] = map[string]readabilityDashboard{
			"full":  serveReadabilityDashboard(t, seedReadabilityFixture),
			"empty": serveReadabilityDashboard(t, func(*testing.T, *registry.Registry, *daemonSurface) {}),
		}
	}
	dashboards, err := json.Marshal(themes)
	if err != nil {
		t.Fatal(err)
	}
	guard := exec.CommandContext(t.Context(), "node", filepath.Join("..", "..", "scripts", "dashboard-guard", "guard.mjs"))
	guard.Env = append(os.Environ(), "AGENTHAIL_GUARD_DASHBOARDS="+string(dashboards))
	guard.Stdout = os.Stdout
	guard.Stderr = os.Stderr
	if err := guard.Run(); err != nil {
		t.Fatalf("readability guard: %v", err)
	}
}

type followingSurface struct{ *daemonSurface }

func (f followingSurface) Stream(ctx context.Context, session *surface.Session, cursor string, onEvent func(surface.StreamEvent), idle time.Duration) error {
	if err := f.daemonSurface.Stream(ctx, session, cursor, onEvent, idle); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}

func serveReadabilityDashboard(t *testing.T, seed func(*testing.T, *registry.Registry, *daemonSurface)) readabilityDashboard {
	t.Helper()
	store, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	fake := &daemonSurface{sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}, accepted: true}
	seed(t, store, fake)
	d := newTestDaemon(t, store, []surface.Surface{followingSurface{fake}})
	d.discoverCatalog(t.Context())
	paired, err := store.ListHosts()
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range paired {
		if err := d.attachHost(host); err != nil {
			t.Fatal(err)
		}
	}
	cookie, err := signInBrowser(store)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: dashboardTestToken}))
	t.Cleanup(server.Close)
	return readabilityDashboard{URL: server.URL, Cookie: cookie.Name + "=" + cookie.Value, Token: dashboardTestToken}
}

func seedReadabilityFixture(t *testing.T, store *registry.Registry, fake *daemonSurface) {
	t.Helper()
	now := time.Now()
	dialog := &surface.Waiting{Kind: surface.WaitingDialog, ID: "dialog-1", Since: now}
	fake.observations["review"] = &surface.TurnObservation{Status: surface.StatusBusy, WaitingOn: dialog}
	sessions := []surface.Session{
		{ID: "research", Name: "research", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusBusy, Source: "vscode", Transport: "desktop", Cwd: "/work/agenthail", LastActive: now},
		{
			ID: "review", Name: "review", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusBusy, Source: "vscode", Transport: "desktop", Cwd: "/work/agenthail", LastActive: now.Add(-time.Hour),
			WaitingOn:       dialog,
			BackgroundTasks: &surface.BackgroundTasks{Running: true, Source: surface.BackgroundSourceHost, Since: now, Tasks: []surface.BackgroundTask{{ID: "shell-1", Kind: "shell", Description: "go test ./internal/daemon", Since: now}}},
		},
	}
	if err := store.AddProject(registry.ProjectRecord{ID: "p-agenthail", WorkspaceRoot: "/work/agenthail", Title: "agenthail", Added: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddHost(registry.PairedHost{ID: "h0123456789ab", Name: "build-server", Endpoint: "https://build-server.invalid:7412", Address: "100.64.0.2", Token: "ahd_fixture"}); err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		if err := store.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
		fake.sessions[session.ID] = session
	}
	fake.caps = surface.Capabilities{Stream: true}
	fake.streamEvents = []surface.StreamEvent{
		{Kind: "user", Text: "Check the dashboard reads over every background."},
		{Kind: "text", Text: "Every text node sits on a pane whose tint meets the contrast target."},
		{Kind: "tool_use", Text: "go test ./internal/daemon"},
		{Kind: "done"},
	}
	if err := store.QueueMessage("review", "Pick up the readability review when you are idle."); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddRelayRoute("research", "review", "", false, ""); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []registry.HistoryEntry{
		{Kind: "sent", SessionID: "research", Message: "Start on the readability guard.", Result: "delivered"},
		{Kind: "failed", SessionID: "review", Message: "Run the browser matrix.", Error: "the session was busy"},
	} {
		if err := store.RecordHistory(entry); err != nil {
			t.Fatal(err)
		}
	}
}
