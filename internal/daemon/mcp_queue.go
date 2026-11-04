package daemon

import (
	"context"
	"strings"
)

type mcpQueueInput struct {
	mcpWrite
	Action  string `json:"action" jsonschema:"retry or cancel one queued instruction, or clear every pending one for a session."`
	QueueID int64  `json:"queueId,omitempty" jsonschema:"For retry and cancel: the entry's id, as agenthail_history lists it: its queue view for active entries, its history view for older ones."`
	Target  string `json:"target,omitempty" jsonschema:"For clear: the session id or @alias."`
}

func (d *Daemon) mcpQueue(ctx context.Context, caller *mcpCaller, input mcpQueueInput) (any, error) {
	if !caller.has("control.all") {
		return nil, refuse("capability_missing", "The delivery queue spans every session; managing it is for the person's voice operator.")
	}
	request := dashboardActionRequest{QueueID: input.QueueID}
	switch strings.TrimSpace(input.Action) {
	case "retry", "cancel":
		if input.QueueID <= 0 {
			return nil, refuse("invalid", "queueId is required to "+input.Action+".")
		}
		request.Action = "queue-" + strings.TrimSpace(input.Action)
	case "clear":
		target, err := d.mcpResolveSession(input.Target)
		if err != nil {
			return nil, err
		}
		request.Action, request.SessionID = "queue-clear", target.ID
	default:
		return nil, refuse("invalid", "action is retry, cancel or clear.")
	}
	return d.mcpAction(ctx, caller, "agenthail_queue", input.mcpWrite, request)
}
