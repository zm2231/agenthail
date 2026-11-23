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
	Mode    string `json:"mode,omitempty" jsonschema:"queue: delivered when the session is idle. now: steers a busy session where it can, otherwise queues; only for sessions you control. Omitted: now to a session you started or lead or from an identity set to send now, else queue; a refused default now queues with a notice."`
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
	why := "your identity sends now by default"
	if mode != registry.SendModeNow && d.leadsTarget(ctx, caller, input.Target) {
		mode, why = registry.SendModeNow, "a send to a session you started or lead goes now by default"
	}
	input.Mode = mode
	if mode != registry.SendModeNow {
		return d.mcpSendIn(ctx, caller, input, true)
	}
	input.chosen = mode
	key, err := input.key("agenthail_send")
	if err != nil {
		return nil, err
	}
	input.heldAbove = true
	return d.mcpOnce(ctx, caller, key, input, func() (any, error) {
		return d.mcpSendNowElseQueue(ctx, caller, input, why)
	})
}

func (d *Daemon) mcpSendNowElseQueue(ctx context.Context, caller *mcpCaller, input mcpSendInput, why string) (any, error) {
	result, err := d.mcpSendIn(ctx, caller, input, false)
	var refusal *mcpRefusal
	if !errors.As(err, &refusal) || !nowRefused(refusal) {
		return result, err
	}
	input.Mode, input.chosen = registry.SendModeQueue, ""
	if result, err = d.mcpSendIn(ctx, caller, input, true); err != nil {
		return nil, err
	}
	return withSendNotice(result, "Queued: "+why+", but this one could not: "+refusal.Message)
}

// leadsTarget reports whether the caller started or leads the target, the
// sessions delivery lets it steer. A paired host's session is decided by
// that host; this one knows only that the caller coordinates its project.
func (d *Daemon) leadsTarget(ctx context.Context, caller *mcpCaller, ref string) bool {
	if strings.TrimSpace(ref) == "" {
		return false
	}
	session, remote, err := d.mcpTargets(caller).Resolve(ctx, strings.TrimSpace(ref))
	if err != nil {
		return false
	}
	if remote != nil {
		return remote.Exact && caller.project != nil && remote.Project == caller.project.ID && d.mcpIsProjectLead(caller, caller.session.ID)
	}
	return d.oversees(caller.session.ID, *session)
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
