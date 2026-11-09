package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAnAgentPostsOnlyToItsOwnProjectsChannel(t *testing.T) {
	d, r, fake, from, to := daemonFixture(t)
	widget := widgetRepo(t, "https://github.com/example/widget.git")
	gadget := widgetRepo(t, "https://github.com/example/gadget.git")
	outsider := surface.Session{ID: "outsider", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
	operator := outsider
	operator.ID = "operator"
	from.Cwd, to.Cwd, outsider.Cwd, operator.Cwd = widget, widget, gadget, gadget
	for _, session := range []surface.Session{from, to, outsider, operator} {
		session.LastActive = time.Now()
		fake.sessions[session.ID] = session
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	d.discoverCatalog(context.Background())
	server := mcpServer(t, d)
	member := mcpSession(t, server, issueMCPToken(t, d, to))
	foreign := mcpSession(t, server, issueMCPToken(t, d, outsider))
	widgetPosts := func() int {
		count := 0
		for _, message := range fake.sent {
			if strings.Contains(message, " via #widget] ") {
				count++
			}
		}
		return count
	}

	if out, refused := callMCP(t, member, "agenthail_channel_send", map[string]any{"channel": "#widget", "message": "standup", "clientRequestId": "own"}); refused || widgetPosts() != 1 {
		t.Fatalf("a member's post to its own project: %v posts=%d", out, widgetPosts())
	}
	out, refused := callMCP(t, foreign, "agenthail_channel_send", map[string]any{"channel": "#widget", "message": "from gadget", "clientRequestId": "foreign"})
	detail, _ := out["detail"].(map[string]any)
	if !refused || out["code"] != "out_of_scope" || detail["rule"] != "different_project" || widgetPosts() != 1 {
		t.Fatalf("another project's agent posting: refused=%v out=%v posts=%d", refused, out, widgetPosts())
	}

	if out, refused := callMCP(t, asOperator(t, d, server, operator.ID), "agenthail_channel_send", map[string]any{"channel": "#widget", "message": "from the operator", "clientRequestId": "operator"}); refused || widgetPosts() != 3 {
		t.Fatalf("control.all posting to another project: %v posts=%d", out, widgetPosts())
	}
	person := principal{ID: localDashboardPrincipal, Kind: principalPerson}
	if reply := d.doAction(t.Context(), person, dashboardActionRequest{Action: "channel-send", Channel: "#widget", Message: "from the person", Author: surface.AuthorPerson}); reply.Status != 200 || widgetPosts() != 5 {
		t.Fatalf("the person's post: status=%d body=%s posts=%d", reply.Status, reply.Body, widgetPosts())
	}

	cli := principal{ID: localDashboardPrincipal, Kind: principalPerson, Claimed: CLIActor}
	if reply := d.doAction(t.Context(), cli, dashboardActionRequest{Action: "channel-send", Channel: "#widget", Message: "from the CLI", SourceSessionID: "outsider"}); reply.Status != 200 || widgetPosts() != 7 {
		t.Fatalf("the CLI posts as the CLI, as agenthail send does: status=%d body=%s posts=%d", reply.Status, reply.Body, widgetPosts())
	}

	for _, request := range []dashboardActionRequest{{Action: "channel-create", Channel: "ops"}, {Action: "channel-add", Channel: "ops", TargetID: "from"}} {
		if reply := d.doAction(t.Context(), person, request); reply.Status != 200 {
			t.Fatalf("%s status=%d %s", request.Action, reply.Status, reply.Body)
		}
	}
	if out, refused := callMCP(t, foreign, "agenthail_channel_send", map[string]any{"channel": "ops", "message": "a stored channel", "clientRequestId": "stored"}); refused {
		t.Fatalf("a stored channel keeps its members' reach: %v", out)
	}
}
