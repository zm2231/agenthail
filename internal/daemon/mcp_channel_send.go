package daemon

import (
	"context"
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/broadcast"
)

type mcpChannelSendInput struct {
	mcpWrite
	Channel string `json:"channel" jsonschema:"A channel or project name, as agenthail_channel's list or agenthail_projects shows it."`
	Message string `json:"message"`
}

func (d *Daemon) mcpChannelSend(ctx context.Context, caller *mcpCaller, input mcpChannelSendInput) (any, error) {
	if strings.TrimSpace(input.Channel) == "" || strings.TrimSpace(input.Message) == "" {
		return nil, refuse("invalid", "channel and message are required.")
	}
	if target, err := broadcast.Resolve(d.Registry, input.Channel); err == nil && target.ProjectID != "" {
		if refusal := mcpProjectPostScope(caller, target); refusal != nil {
			return nil, refusal
		}
	}
	return d.mcpAction(ctx, caller, "agenthail_channel_send", input.mcpWrite, dashboardActionRequest{Action: "channel-send", Channel: input.Channel, Message: input.Message, SourceSessionID: caller.session.ID})
}

func mcpProjectPostScope(caller *mcpCaller, target broadcast.Target) *mcpRefusal {
	if caller.has("control.all") || caller.project != nil && caller.project.ID == target.ProjectID {
		return nil
	}
	return &mcpRefusal{Code: "out_of_scope", Message: fmt.Sprintf("#%s is project %s, not yours; you may post only to your own project's channel. Reach its coordinator with a queued agenthail_send and project instead.", target.Name, target.Label()), Detail: map[string]any{"rule": "different_project"}}
}
