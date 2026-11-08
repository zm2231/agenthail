package daemon

import (
	"context"
	"encoding/json"
	"strings"
)

type mcpSendInput struct {
	mcpWrite
	Target  string `json:"target,omitempty" jsonschema:"A session id or @alias. Give exactly one of target or project."`
	Project string `json:"project,omitempty" jsonschema:"A project name or id, as agenthail_projects lists it, to reach its coordinator (starting its conversation if it has none)."`
	Message string `json:"message"`
	Mode    string `json:"mode,omitempty" jsonschema:"queue (default): delivered when the session is idle. now: steers a busy session where it can, otherwise queues; only for sessions you control."`
}

func (d *Daemon) mcpSend(ctx context.Context, caller *mcpCaller, input mcpSendInput) (any, error) {
	if strings.TrimSpace(input.Message) == "" {
		return nil, refuse("invalid", "message is required.")
	}
	mode := strings.TrimSpace(input.Mode)
	if mode == "" {
		mode = "queue"
	}
	if mode != "queue" && mode != "now" {
		return nil, refuse("invalid", "mode is queue or now.")
	}
	if (strings.TrimSpace(input.Target) == "") == (strings.TrimSpace(input.Project) == "") {
		return nil, refuse("invalid", "Give exactly one of target or project.")
	}
	if strings.TrimSpace(input.Project) != "" {
		return d.mcpSendToLead(ctx, caller, input)
	}
	target, err := d.mcpControlTarget(ctx, caller, input.Target, mode == "queue")
	if err != nil {
		return nil, err
	}
	busyDelivery := "queue"
	if mode == "now" {
		busyDelivery = "steer"
	}
	result, err := d.mcpAction(ctx, caller, "agenthail_send", input.mcpWrite, dashboardActionRequest{Action: "send", SessionID: target.ID, Message: input.Message, SourceSessionID: caller.session.ID, BusyDelivery: busyDelivery})
	if err != nil {
		return nil, err
	}
	return json.RawMessage(result), nil
}
