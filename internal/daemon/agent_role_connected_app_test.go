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
	"github.com/zm2231/agenthail/internal/agenttools"
	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/daemonapi"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAConnectedAppReadsAndQueuesToACoordinatorButCannotControlOrSpawn(t *testing.T) {
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
	if _, err := r.AddAgentIdentity("scout"); err != nil {
		t.Fatal(err)
	}
	_, token, err := r.BindRemoteIdentity("scout", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.changeAgentRole(context.Background(), daemonapi.CLIActor, daemonapi.AgentRoleRequest{Op: "assign", Name: agentroles.ConnectedApp, Kind: registry.AssignIdentity, Target: "scout"}); err != nil {
		t.Fatal(err)
	}
	server := mcpServer(t, d)
	transport := &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: headerTransport{"Authorization": "Bearer " + token}}, MaxRetries: -1}
	app, err := mcp.NewClient(&mcp.Implementation{Name: "app", Version: "1"}, nil).Connect(t.Context(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })

	listed, err := app.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, tool := range listed.Tools {
		if _, agenthail := agenttools.Lookup(tool.Name); agenthail {
			seen = append(seen, tool.Name)
		}
	}
	want := slices.Clone(agentroles.BuiltIn[agentroles.ConnectedApp].Tools)
	slices.Sort(seen)
	slices.Sort(want)
	if !slices.Equal(seen, want) {
		t.Fatalf("a connected app sees %v, want %v", seen, want)
	}
	if out, refused := callMCP(t, app, "agenthail_whoami", nil); refused || out["role"].(map[string]any)["name"] != agentroles.ConnectedApp {
		t.Fatalf("whoami: %v", out)
	}
	if out, refused := callMCP(t, app, "agenthail_read", map[string]any{}); refused {
		t.Fatalf("reading its own inbox was refused: %v", out)
	}
	if out, refused := callMCP(t, app, "agenthail_create", map[string]any{"surface": "codex", "message": "spawn"}); !refused || out["code"] != "capability_missing" {
		t.Fatalf("a connected app started a session: %v", out)
	}
	if out, refused := callMCP(t, app, "agenthail_send", map[string]any{"target": from.ID, "message": "direct", "clientRequestId": "direct"}); !refused {
		t.Fatalf("a connected app messaged a session directly: %v", out)
	}
	if out, refused := callMCP(t, app, "agenthail_send", map[string]any{"project": "widget", "message": "steer the lead", "mode": "now", "clientRequestId": "now"}); !refused {
		t.Fatalf("a connected app sent now to a coordinator: %v", out)
	}
	if out, refused := callMCP(t, app, "agenthail_send", map[string]any{"project": "widget", "message": "queued for the lead", "clientRequestId": "queued"}); refused {
		t.Fatalf("a connected app could not queue to the coordinator: %v", out)
	}
	if !slices.ContainsFunc(fake.sent, func(message string) bool { return strings.Contains(message, "queued for the lead") }) || slices.ContainsFunc(fake.sent, func(message string) bool { return strings.Contains(message, "direct") || strings.Contains(message, "steer the lead") }) {
		t.Fatalf("delivered: %q", fake.sent)
	}
}
