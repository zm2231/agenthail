package daemon

import (
	"context"
	"strings"
)

type mcpChannelSendInput struct {
	mcpWrite
	Channel string `json:"channel" jsonschema:"A channel or project name, as agenthail channel list shows it."`
	Message string `json:"message"`
}

func (d *Daemon) mcpChannelSend(ctx context.Context, caller *mcpCaller, input mcpChannelSendInput) (any, error) {
	if strings.TrimSpace(input.Channel) == "" || strings.TrimSpace(input.Message) == "" {
		return nil, refuse("invalid", "channel and message are required.")
	}
	return d.mcpAction(ctx, caller, "agenthail_channel_send", input.mcpWrite, dashboardActionRequest{Action: "channel-send", Channel: input.Channel, Message: input.Message, SourceSessionID: caller.session.ID})
}
