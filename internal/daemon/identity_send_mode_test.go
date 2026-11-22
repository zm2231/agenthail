package daemon

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/agentroles"
	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/daemonapi"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func bindRemoteIdentity(t *testing.T, r *registry.Registry, name string) string {
	t.Helper()
	if _, err := r.AddAgentIdentity(name); err != nil {
		t.Fatal(err)
	}
	_, token, err := r.BindRemoteIdentity(name, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// Binding raises the remote epoch, which retires connected clients, so connect only after every bind.
func remoteIdentityClient(t *testing.T, server, token string) *mcp.ClientSession {
	t.Helper()
	transport := &mcp.StreamableClientTransport{Endpoint: server + "/mcp", HTTPClient: &http.Client{Transport: headerTransport{"Authorization": "Bearer " + token}}, MaxRetries: -1, DisableStandaloneSSE: true}
	client, err := mcp.NewClient(&mcp.Implementation{Name: "identity", Version: "1"}, nil).Connect(t.Context(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func TestAnIdentitySetToSendNowSteersABusySessionUnlessItSaysQueue(t *testing.T) {
	d, r, fake, _, to := daemonFixture(t)
	fake.caps = surface.Capabilities{Send: true, Steer: true}
	fake.rejectBusy = true
	to.Status = surface.StatusBusy
	fake.sessions[to.ID] = to
	fake.observations[to.ID] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-1"}
	if err := r.RecordObservedSession(to); err != nil {
		t.Fatal(err)
	}
	server := mcpServer(t, d)
	katToken, scoutToken := bindRemoteIdentity(t, r, "kat"), bindRemoteIdentity(t, r, "scout")
	kat, scout := remoteIdentityClient(t, server.URL, katToken), remoteIdentityClient(t, server.URL, scoutToken)
	if _, err := d.setIdentitySettings(context.Background(), daemonapi.CLIActor, daemonapi.IdentitySettingsRequest{Identity: "kat", SendMode: registry.SendModeNow}); err != nil {
		t.Fatal(err)
	}

	if out, refused := callMCP(t, kat, "agenthail_send", map[string]any{"target": to.ID, "message": "kat by default"}); refused || out["notice"] != nil {
		t.Fatalf("kat's send with no mode: %v", out)
	}
	if out, refused := callMCP(t, kat, "agenthail_send", map[string]any{"target": to.ID, "message": "kat says queue", "mode": "queue"}); refused {
		t.Fatalf("kat's queued send: %v", out)
	}
	if out, refused := callMCP(t, scout, "agenthail_send", map[string]any{"target": to.ID, "message": "scout by default"}); refused {
		t.Fatalf("scout's send with no mode: %v", out)
	}
	d.drainSteerMessageQueue(t.Context(), fake, &to)
	steered := func(text string) bool {
		return slices.ContainsFunc(fake.steered, func(m string) bool { return strings.Contains(m, text) })
	}
	if out, refused := callMCP(t, scout, "agenthail_steer", map[string]any{"target": to.ID, "message": "scout steers"}); refused || !steered("scout steers") {
		t.Fatalf("a remote identity's steer within its reach: %v, steered=%q", out, fake.steered)
	}
	if !steered("kat by default") || steered("kat says queue") || steered("scout by default") || r.QueueCount(to.ID) != 2 {
		t.Fatalf("only kat's send with no mode should steer: steered=%q queued=%d", fake.steered, r.QueueCount(to.ID))
	}
}

func TestAnIdentitysDefaultNowFallsBackToQueuedWhereItsRoleMayNotSteer(t *testing.T) {
	d, r, fake, from, to := daemonFixture(t)
	widget := widgetRepo(t, "https://github.com/example/widget.git")
	from.Cwd, to.Cwd = widget, widget
	for _, session := range []surface.Session{from, to} {
		session.LastActive = time.Now()
		fake.sessions[session.ID] = session
		if err := r.RecordObservedSession(session); err != nil {
			t.Fatal(err)
		}
	}
	d.discoverCatalog(context.Background())
	project, err := broadcast.FindProject(r, "widget")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetCoordinator(registry.CoordinatorSpec{ProjectID: project.ID, ProjectRoot: widget, Name: "widget-lead", Surface: string(surface.KindClaude), Cwd: widget}, registry.CoordinatorChange{SessionID: to.ID, ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	app := remoteIdentityClient(t, mcpServer(t, d).URL, bindRemoteIdentity(t, r, "kat"))
	for _, change := range []daemonapi.AgentRoleRequest{
		{Op: "put", Name: "asks-to-steer", Role: agentroles.Role{Reach: agentroles.ReachDescendants, Actions: map[string]string{"cross-project-steer": agentroles.Ask}}},
		{Op: "assign", Name: "asks-to-steer", Kind: registry.AssignIdentity, Target: "kat"},
	} {
		if err := d.changeAgentRole(context.Background(), daemonapi.CLIActor, change); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.setIdentitySettings(context.Background(), daemonapi.CLIActor, daemonapi.IdentitySettingsRequest{Identity: "kat", SendMode: registry.SendModeNow}); err != nil {
		t.Fatal(err)
	}

	if out, refused := callMCP(t, app, "agenthail_send", map[string]any{"project": "widget", "message": "explicitly now", "mode": "now"}); !refused {
		t.Fatalf("a role that asks to steer steered a coordinator: %v", out)
	}
	out, refused := callMCP(t, app, "agenthail_send", map[string]any{"project": "widget", "message": "now by default", "clientRequestId": "fallback"})
	notice, _ := out["notice"].(string)
	if refused || !strings.HasPrefix(notice, "Queued: your identity sends now by default") {
		t.Fatalf("a defaulted now the role refuses: %v", out)
	}
	if !slices.ContainsFunc(fake.sent, func(m string) bool { return strings.Contains(m, "now by default") }) {
		t.Fatalf("the fallback did not deliver: sent=%q", fake.sent)
	}
	if replay, refused := callMCP(t, app, "agenthail_send", map[string]any{"project": "widget", "message": "now by default", "clientRequestId": "fallback"}); refused || replay["notice"] != notice {
		t.Fatalf("retrying the same request: %v", replay)
	}
	if got := slices.DeleteFunc(slices.Clone(fake.sent), func(m string) bool { return !strings.Contains(m, "now by default") }); len(got) != 1 {
		t.Fatalf("the retry delivered again: %q", got)
	}
}
