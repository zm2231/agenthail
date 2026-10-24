package daemon

import (
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
	full := serveReadabilityDashboard(t, seedReadabilityFixture)
	empty := serveReadabilityDashboard(t, func(*testing.T, *registry.Registry, *daemonSurface) {})
	dashboards, err := json.Marshal(map[string]readabilityDashboard{"full": full, "empty": empty})
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

func serveReadabilityDashboard(t *testing.T, seed func(*testing.T, *registry.Registry, *daemonSurface)) readabilityDashboard {
	t.Helper()
	store, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	fake := &daemonSurface{sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}, accepted: true}
	seed(t, store, fake)
	d := newTestDaemon(t, store, []surface.Surface{fake})
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
	sessions := []surface.Session{
		{ID: "research", Name: "research", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusBusy, Source: "vscode", Transport: "desktop", Cwd: "/work/agenthail", LastActive: now},
		{ID: "review", Name: "review", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", Cwd: "/work/agenthail", LastActive: now.Add(-time.Hour)},
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
