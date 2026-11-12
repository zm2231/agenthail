package daemon

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type mcpAnswerInput struct {
	mcpWrite
	RequestID string            `json:"requestId" jsonschema:"The open request's id, as agenthail_requests lists it."`
	SessionID string            `json:"sessionId,omitempty" jsonschema:"The session that holds the request, for one on another host, as its notice names it."`
	Decision  string            `json:"decision,omitempty" jsonschema:"A permission request: allow, always (keep allowing with the rule the request offers; for the person's voice operator only), deny, never (keep denying) or stop (deny and end the turn). Any other ask: the id of the option you pick, as agenthail_requests lists it, or omit it and give answers or a message. Any: escalate passes it on."`
	Message   string            `json:"message,omitempty" jsonschema:"With deny: why, which the session reads. On an agent's ask: a note."`
	Answers   map[string]string `json:"answers,omitempty" jsonschema:"One answer per question, keyed by its text."`
	Content   json.RawMessage   `json:"content,omitempty" jsonschema:"With allow on an MCP server's input request: its form's field values."`
}

// An ask routed to a session is that session's to answer; the routing, not a
// capability, grants it. A permission request still needs the capability to
// answer requests at all.
func (d *Daemon) mcpAnswer(ctx context.Context, caller *mcpCaller, input mcpAnswerInput) (any, error) {
	askID := strings.TrimSpace(input.RequestID)
	if askID == "" {
		return nil, refuse("invalid", "requestId is required.")
	}
	ask, found, err := d.Registry.Ask(askID)
	if err != nil {
		return nil, err
	}
	response := surface.Response{Message: strings.TrimSpace(input.Message), Answers: input.Answers, Content: input.Content}
	choice := strings.TrimSpace(input.Decision)
	if choice != "" {
		response.Choices = []string{choice}
	}
	if !found {
		return d.mcpAnswerOnHost(ctx, caller, input, askID, choice, response)
	}
	if ask.Origin == registry.AskOriginPermission && choice != askEscalate {
		if !caller.has("requests.answer") {
			return nil, refuse("capability_missing", "This session may not answer requests.")
		}
		if choice == "always" && !caller.has("control.all") {
			return nil, (&mcpRefusal{Code: "exceeds_ceiling", Message: "Always allow adds a lasting rule that can raise the session's permission mode; allow this one request instead.", Detail: map[string]any{"axis": "permission"}}).about(ask.SessionID)
		}
	}
	p := sessionPrincipal(caller.session.ID)
	p.ForPerson = caller.has("control.all")
	return d.mcpActionAs(ctx, caller, p, "agenthail_answer", input.mcpWrite, dashboardActionRequest{Action: "respond", SessionID: ask.SessionID, RequestID: askID, Decision: response})
}

func (d *Daemon) mcpAnswerOnHost(ctx context.Context, caller *mcpCaller, input mcpAnswerInput, askID, choice string, response surface.Response) (any, error) {
	missing := refuse("not_found", "No open request "+askID+".")
	if strings.TrimSpace(input.SessionID) == "" {
		return nil, missing
	}
	_, remote, err := d.mcpTargets(caller).Resolve(ctx, strings.TrimSpace(input.SessionID))
	if err != nil || remote == nil || !remote.Exact {
		return nil, missing
	}
	if choice != askEscalate && !caller.has("requests.answer") {
		return nil, refuse("capability_missing", "This session may not answer requests.")
	}
	if choice == "always" && !caller.has("control.all") {
		return nil, (&mcpRefusal{Code: "exceeds_ceiling", Message: "Always allow adds a lasting rule that can raise the session's permission mode; allow this one request instead.", Detail: map[string]any{"axis": "permission"}}).about(remote.ID)
	}
	return d.mcpActionAs(ctx, caller, sessionPrincipal(caller.session.ID), "agenthail_answer", input.mcpWrite, dashboardActionRequest{Action: "respond", SessionID: remote.ID, RequestID: askID, Decision: response, SourceSessionID: caller.session.ID})
}
