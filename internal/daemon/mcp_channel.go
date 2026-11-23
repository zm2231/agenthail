package daemon

import (
	"context"
	"strings"
)

type mcpChannelInput struct {
	mcpWrite
	Action  string `json:"action" jsonschema:"create, delete, rename, add, remove or list."`
	Channel string `json:"channel,omitempty" jsonschema:"The channel's name, with or without #."`
	NewName string `json:"newName,omitempty" jsonschema:"For rename: the new name."`
	Target  string `json:"target,omitempty" jsonschema:"For add and remove: the member's session id or @alias."`
}

var mcpChannelActions = map[string]string{"create": "channel-create", "delete": "channel-delete", "rename": "channel-rename", "add": "channel-add", "remove": "channel-remove"}

func (d *Daemon) mcpChannel(ctx context.Context, caller *mcpCaller, input mcpChannelInput) (any, error) {
	notice, err := roleGate(caller, "channel-any", "Changed a channel", "Channels span projects; managing them is for the person. Post to one with agenthail_channel_send.")
	if err != nil {
		return nil, err
	}
	verb := strings.TrimSpace(input.Action)
	if verb == "list" {
		channels, err := d.Registry.ListChannels()
		if err != nil {
			return nil, err
		}
		return map[string]any{"channels": channels}, nil
	}
	action, ok := mcpChannelActions[verb]
	if !ok {
		return nil, refuse("invalid", "action is create, delete, rename, add, remove or list.")
	}
	if strings.TrimSpace(input.Channel) == "" {
		return nil, refuse("invalid", "channel is required.")
	}
	request := dashboardActionRequest{Action: action, Channel: input.Channel, NewChannel: input.NewName}
	if verb == "add" || verb == "remove" {
		target, err := d.mcpResolveSession(input.Target)
		if err != nil {
			return nil, err
		}
		request.TargetID = target.ID
	}
	result, err := d.mcpAction(ctx, caller, "agenthail_channel", input.mcpWrite, request)
	d.noticeAfter(ctx, caller, notice, err)
	return result, err
}
