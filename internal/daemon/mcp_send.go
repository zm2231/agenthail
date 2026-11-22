package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type mcpSendInput struct {
	mcpWrite
	Target  string `json:"target,omitempty" jsonschema:"A session id or @alias. Give exactly one of target or project."`
	Project string `json:"project,omitempty" jsonschema:"A project name or id, as agenthail_projects lists it, to reach its coordinator (starting its conversation if it has none)."`
	Message string `json:"message"`
	Mode    string `json:"mode,omitempty" jsonschema:"queue: delivered when the session is idle. now: steers a busy session where it can, otherwise queues; only for sessions you control. Omitted, it is queue, or now for an identity the person set to send now, which falls back to queue where now is refused."`
	mcpAskPerson
	chosen string
}

func (d *Daemon) mcpSend(ctx context.Context, caller *mcpCaller, input mcpSendInput) (any, error) {
	if strings.TrimSpace(input.Message) == "" {
		return nil, refuse("invalid", "message is required.")
	}
	input.Mode = strings.TrimSpace(input.Mode)
	if input.Mode != "" && input.Mode != registry.SendModeQueue && input.Mode != registry.SendModeNow {
		return nil, refuse("invalid", "mode is queue or now.")
	}
	if (strings.TrimSpace(input.Target) == "") == (strings.TrimSpace(input.Project) == "") {
		return nil, refuse("invalid", "Give exactly one of target or project.")
	}
	input.chosen = input.Mode
	if input.Mode != "" {
		return d.mcpSendIn(ctx, caller, input, true)
	}
	mode, err := d.callerSendMode(caller)
	if err != nil {
		return nil, err
	}
	input.Mode = mode
	if mode != registry.SendModeNow {
		return d.mcpSendIn(ctx, caller, input, true)
	}
	input.chosen = mode
	result, err := d.mcpSendIn(ctx, caller, input, false)
	var refusal *mcpRefusal
	if !errors.As(err, &refusal) || !nowRefused(refusal) {
		return result, err
	}
	input.Mode, input.chosen = registry.SendModeQueue, ""
	if input.ClientRequestID != "" {
		input.ClientRequestID += ":queued"
	}
	if result, err = d.mcpSendIn(ctx, caller, input, true); err != nil {
		return nil, err
	}
	return withSendNotice(result, "Queued: your identity sends now by default, but this one could not: "+refusal.Message)
}

// Only a refusal of the steer itself falls back; a failed delivery is returned as it is.
func nowRefused(refusal *mcpRefusal) bool {
	return refusal.Code == "out_of_scope" || refusal.actionCode == "out_of_scope" || refusal.actionCode == "host_unreachable"
}

func (d *Daemon) callerSendMode(caller *mcpCaller) (string, error) {
	if !surface.IsAgentIdentityID(caller.session.ID) {
		return registry.SendModeQueue, nil
	}
	return d.Registry.IdentitySendMode(caller.session.ID)
}

func (d *Daemon) mcpSendIn(ctx context.Context, caller *mcpCaller, input mcpSendInput, askable bool) (any, error) {
	if strings.TrimSpace(input.Project) != "" {
		return d.mcpSendToLead(ctx, caller, input, busyDeliveryFor(input.chosen))
	}
	busyDelivery := "queue"
	if input.Mode == registry.SendModeNow {
		busyDelivery = "steer"
	}
	reach := reachSteer
	if input.Mode == registry.SendModeQueue {
		reach = reachQueued
	}
	target, notice, err := d.mcpReachTarget(ctx, caller, input.Target, reach)
	if err != nil && !askable {
		return nil, err
	}
	if err != nil {
		return d.mcpAskOnRefusal(ctx, caller, err, input.mcpAskPerson, dashboardActionRequest{Action: "send", Message: input.Message, BusyDelivery: busyDelivery})
	}
	if reach == reachSteer {
		ctx = surface.WithSteerAdmitted(ctx, caller.session.ID)
	}
	result, err := d.mcpAction(ctx, caller, "agenthail_send", input.mcpWrite, dashboardActionRequest{Action: "send", SessionID: target.ID, Message: input.Message, SourceSessionID: caller.session.ID, BusyDelivery: busyDelivery})
	d.noticeAfter(ctx, caller, notice, err)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(result), nil
}

func busyDeliveryFor(mode string) string {
	switch mode {
	case registry.SendModeNow:
		return "steer"
	case registry.SendModeQueue:
		return "queue"
	}
	return ""
}

func withSendNotice(result any, notice string) (any, error) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	fields["notice"], _ = json.Marshal(notice)
	return fields, nil
}
