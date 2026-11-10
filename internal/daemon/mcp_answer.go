package daemon

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

type mcpAnswerInput struct {
	mcpWrite
	Target    string            `json:"target" jsonschema:"A session id or @alias you spawned or, as coordinator, lead."`
	RequestID string            `json:"requestId" jsonschema:"The open request's id, as agenthail_requests lists it."`
	Decision  string            `json:"decision" jsonschema:"allow, always (keep allowing with the rule the request offers; for the person's voice operator only), deny, never (keep denying) or stop (deny and end the turn)."`
	Message   string            `json:"message,omitempty" jsonschema:"With deny: why, which the session reads."`
	Answers   map[string]string `json:"answers,omitempty" jsonschema:"With allow on a question: one answer per question, keyed by its text."`
	Content   json.RawMessage   `json:"content,omitempty" jsonschema:"With allow on an MCP server's input request: its form's field values."`
}

func (d *Daemon) mcpAnswer(ctx context.Context, caller *mcpCaller, input mcpAnswerInput) (any, error) {
	if strings.TrimSpace(input.RequestID) == "" {
		return nil, refuse("invalid", "requestId is required.")
	}
	if requestID := strings.TrimSpace(input.RequestID); isAskID(requestID) {
		return d.mcpAnswerAsk(ctx, caller, requestID, input)
	}
	if !caller.has("requests.answer") {
		return nil, refuse("capability_missing", "This session may not answer requests.")
	}
	decision := surface.Decision{Message: strings.TrimSpace(input.Message), Answers: input.Answers, Content: input.Content}
	switch strings.TrimSpace(input.Decision) {
	case "allow":
		decision.Behavior = "allow"
	case "always":
		if !caller.has("control.all") {
			return nil, (&mcpRefusal{Code: "exceeds_ceiling", Message: "Always allow adds a lasting rule that can raise the session's permission mode; allow this one request instead.", Detail: map[string]any{"axis": "permission"}}).about(strings.TrimSpace(input.Target))
		}
		decision.Behavior, decision.Always = "allow", true
	case "deny":
		decision.Behavior = "deny"
	case "never":
		decision.Behavior, decision.Always = "deny", true
	case "stop":
		decision.Behavior, decision.Stop = "deny", true
	default:
		return nil, refuse("invalid", "decision is allow, always, deny, never or stop.")
	}
	target, err := d.mcpControlTarget(ctx, caller, input.Target, false)
	if err != nil {
		return nil, err
	}
	return d.mcpAction(ctx, caller, "agenthail_answer", input.mcpWrite, dashboardActionRequest{Action: "respond", SessionID: target.ID, RequestID: strings.TrimSpace(input.RequestID), Decision: decision})
}

// An ask routed to a session is that session's to answer; the routing, not a capability, grants it.
func (d *Daemon) mcpAnswerAsk(ctx context.Context, caller *mcpCaller, askID string, input mcpAnswerInput) (any, error) {
	ask, found, err := d.Registry.Ask(askID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, refuse("not_found", "No ask "+askID+".")
	}
	decision := surface.Decision{Message: strings.TrimSpace(input.Message), Answers: input.Answers}
	switch strings.TrimSpace(input.Decision) {
	case askEscalate:
		decision = surface.Decision{Behavior: askEscalate}
	case "", "allow":
	default:
		return nil, refuse("invalid", "An ask takes answers or a message, or decision escalate.")
	}
	return d.mcpAction(ctx, caller, "agenthail_answer", input.mcpWrite, dashboardActionRequest{Action: "respond", SessionID: ask.SessionID, RequestID: askID, Decision: decision})
}
