package daemon

import (
	"context"
	"strings"
)

type mcpQueueInput struct {
	mcpWrite
	Action  string `json:"action" jsonschema:"cancel one pending instruction, or clear every pending one for a session. A failed one is an ask: answer it with agenthail_answer."`
	QueueID int64  `json:"queueId,omitempty" jsonschema:"For cancel: the pending entry's id, as agenthail_history lists it in its queue view."`
	Target  string `json:"target,omitempty" jsonschema:"For clear: the session id or @alias."`
}

func (d *Daemon) mcpQueue(ctx context.Context, caller *mcpCaller, input mcpQueueInput) (any, error) {
	if !caller.has("control.all") {
		return nil, refuse("capability_missing", "The delivery queue spans every session; managing it is for the person's voice operator.")
	}
	request := dashboardActionRequest{QueueID: input.QueueID}
	switch strings.TrimSpace(input.Action) {
	case "cancel":
		if input.QueueID <= 0 {
			return nil, refuse("invalid", "queueId is required to cancel.")
		}
		request.Action = "queue-cancel"
	case "clear":
		target, err := d.mcpResolveSession(input.Target)
		if err != nil {
			return nil, err
		}
		request.Action, request.SessionID = "queue-clear", target.ID
	default:
		return nil, refuse("invalid", "action is cancel or clear.")
	}
	return d.mcpAction(ctx, caller, "agenthail_queue", input.mcpWrite, request)
}
