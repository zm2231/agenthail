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
	Decision  string            `json:"decision,omitempty" jsonschema:"A permission request: allow, always (keep allowing with the rule the request offers; for the person's voice operator only), deny, never (keep denying) or stop (deny and end the turn). An agent's ask: omit it and give answers or a message. A delivery ask: the option id it offers (retry, delete, cancel or dismiss). Any: escalate passes it on."`
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
	if !found {
		return nil, refuse("not_found", "No open request "+askID+".")
	}
	decision := surface.Decision{Message: strings.TrimSpace(input.Message), Answers: input.Answers, Content: input.Content}
	choice := strings.TrimSpace(input.Decision)
	if choice == askEscalate {
		decision = surface.Decision{Behavior: askEscalate}
	} else if ask.Origin == registry.AskOriginPermission {
		if !caller.has("requests.answer") {
			return nil, refuse("capability_missing", "This session may not answer requests.")
		}
		switch choice {
		case "allow":
			decision.Behavior = "allow"
		case "always":
			if !caller.has("control.all") {
				return nil, (&mcpRefusal{Code: "exceeds_ceiling", Message: "Always allow adds a lasting rule that can raise the session's permission mode; allow this one request instead.", Detail: map[string]any{"axis": "permission"}}).about(ask.SessionID)
			}
			decision.Behavior, decision.Always = "allow", true
		case "deny":
			decision.Behavior = "deny"
		case "never":
			decision.Behavior, decision.Always = "deny", true
		case "stop":
			decision.Behavior, decision.Stop = "deny", true
		default:
			return nil, refuse("invalid", "decision is allow, always, deny, never, stop or escalate.")
		}
	} else if ask.Origin == registry.AskOriginDelivery {
		decision.Behavior = choice
	} else if choice != "" && choice != "allow" {
		return nil, refuse("invalid", "An ask takes answers or a message, or decision escalate.")
	}
	p := sessionPrincipal(caller.session.ID)
	p.ForPerson = caller.has("control.all")
	return d.mcpActionAs(ctx, caller, p, "agenthail_answer", input.mcpWrite, dashboardActionRequest{Action: "respond", SessionID: ask.SessionID, RequestID: askID, Decision: decision})
}
