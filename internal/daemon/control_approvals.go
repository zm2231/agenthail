package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/zm2231/agenthail/internal/registry"
)

var controlApprovalActions = []string{"send", "steer", "interrupt", "session-archive", "session-unarchive"}

func crossProject(refusal *mcpRefusal) *mcpRefusal {
	refusal.crossProject = true
	return refusal
}

func approvableControl(err error) (*mcpRefusal, bool) {
	var refusal *mcpRefusal
	if !errors.As(err, &refusal) || !refusal.crossProject {
		return nil, false
	}
	if refusal.Detail == nil {
		refusal.Detail = map[string]any{}
	}
	refusal.Detail["approvable"] = true
	return refusal, true
}

// controlApproval rebuilds the body an approved send or control runs from the
// fields that action needs, so the asker is always its source and nothing it
// adds rides along.
func controlApproval(caller *mcpCaller, request dashboardActionRequest) (dashboardActionRequest, error) {
	body := dashboardActionRequest{Action: request.Action, SessionID: strings.TrimSpace(request.SessionID)}
	if body.SessionID == "" {
		return dashboardActionRequest{}, refuse("invalid", "the payload names no session.")
	}
	switch request.Action {
	case "send", "steer":
		if strings.TrimSpace(request.Message) == "" {
			return dashboardActionRequest{}, refuse("invalid", "message is required.")
		}
		body.Message, body.SourceSessionID = request.Message, caller.session.ID
		if request.Action == "send" {
			body.BusyDelivery, body.NoQueue = "queue", request.NoQueue
			if request.BusyDelivery == "steer" {
				body.BusyDelivery = "steer"
			}
		}
	case "interrupt", "session-archive", "session-unarchive":
	default:
		return dashboardActionRequest{}, refuse("invalid", "the action "+request.Action+" cannot be asked for.")
	}
	return body, nil
}

func queuedSend(body dashboardActionRequest) bool {
	return body.Action == "send" && body.BusyDelivery == "queue" && !body.NoQueue
}

func (d *Daemon) controlApprovalTarget(ctx context.Context, caller *mcpCaller, body dashboardActionRequest) error {
	_, err := d.mcpControlTarget(ctx, caller, body.SessionID, queuedSend(body))
	return err
}

// askPersonForControl raises an approval for a send or control the caller
// was refused only because its target is in another project, checked again
// from the body that will run.
func (d *Daemon) askPersonForControl(ctx context.Context, caller *mcpCaller, request dashboardActionRequest, reason string) (registry.Ask, error) {
	body, err := controlApproval(caller, request)
	if err != nil {
		return registry.Ask{}, err
	}
	err = d.controlApprovalTarget(ctx, caller, body)
	if err == nil {
		return registry.Ask{}, refuse("invalid", "That is within your reach; run it without asking the person.")
	}
	if _, ok := approvableControl(err); !ok {
		return registry.Ask{}, err
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return registry.Ask{}, err
	}
	return d.askPersonToAllow(ctx, caller, PersonAction{API: PersonActionAPI, Body: encoded}, reason)
}

type mcpAskPerson struct {
	AskPerson bool   `json:"askPerson,omitempty" jsonschema:"If refused because the target is in another project, ask the person to allow it; the answer arrives as your next message."`
	Reason    string `json:"reason,omitempty" jsonschema:"With askPerson: why, shown to the person."`
}

func (d *Daemon) mcpAskOnRefusal(ctx context.Context, caller *mcpCaller, err error, ask mcpAskPerson, request dashboardActionRequest) (any, error) {
	refusal, ok := approvableControl(err)
	if !ok {
		return nil, err
	}
	if !ask.AskPerson {
		refusal.Message += " To ask the person to allow it, call again with askPerson: true and a reason; their answer arrives as your next message."
		return nil, refusal
	}
	request.SessionID = refusal.target
	asked, err := d.askPersonForControl(ctx, caller, request, ask.Reason)
	if err != nil {
		return nil, err
	}
	return map[string]any{"asked": true, "requestId": asked.ID, "detail": "Asked the person to allow this. Their answer, and the result if they allow it, arrives as your next message."}, nil
}

// controlPayloadScope answers the CLI's send or control check from the body
// the command would run, so an approvable refusal can be asked for as is.
func (d *Daemon) controlPayloadScope(ctx context.Context, caller *mcpCaller, request ScopeRequest) (ScopeReply, error) {
	var asked dashboardActionRequest
	if request.Payload.API != PersonActionAPI || json.Unmarshal(request.Payload.Body, &asked) != nil || !slices.Contains(controlApprovalActions, asked.Action) {
		return ScopeReply{}, refuse("invalid", "the payload is a send, steer, interrupt, session-archive or session-unarchive action body.")
	}
	body, err := controlApproval(caller, asked)
	if err != nil {
		return ScopeReply{}, err
	}
	if body.SessionID == caller.session.ID {
		return ScopeReply{TargetID: caller.session.ID}, nil
	}
	target, err := d.mcpControlTarget(ctx, caller, body.SessionID, queuedSend(body))
	if err == nil {
		reply := ScopeReply{TargetID: target.ID}
		if body.Action == "send" {
			reply.ScopeProjectID, err = d.remoteSendScope(caller, target)
		}
		return reply, err
	}
	refusal, ok := approvableControl(err)
	if !ok {
		return ScopeReply{}, err
	}
	if !request.AskPerson {
		refusal.Message += " To ask the person to allow it, run the same command again with --ask-person --reason \"<why you need it>\"; their answer arrives as your next message."
		return ScopeReply{}, refusal
	}
	ask, err := d.askPersonForControl(ctx, caller, body, request.Reason)
	if err != nil {
		return ScopeReply{}, err
	}
	return ScopeReply{AskID: ask.ID}, nil
}
