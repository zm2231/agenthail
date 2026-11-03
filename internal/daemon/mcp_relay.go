package daemon

import (
	"context"
	"strings"
)

type mcpRelayInput struct {
	mcpWrite
	Action  string `json:"action" jsonschema:"add, remove or list."`
	From    string `json:"from,omitempty" jsonschema:"For add: the session whose replies are relayed."`
	To      string `json:"to,omitempty" jsonschema:"For add: the session that receives them."`
	Pattern string `json:"pattern,omitempty" jsonschema:"For add: a regular expression a reply must match; default every reply."`
	Once    bool   `json:"once,omitempty" jsonschema:"For add: stop after the first relayed reply."`
	When    string `json:"when,omitempty" jsonschema:"For add: goal_completed relays when the source's goal completes instead of on each reply."`
	RelayID int64  `json:"relayId,omitempty" jsonschema:"For remove: the relay's id, as list shows it."`
}

func (d *Daemon) mcpRelay(ctx context.Context, caller *mcpCaller, input mcpRelayInput) (any, error) {
	if !caller.has("control.all") {
		return nil, refuse("capability_missing", "Relays route one session's replies into another; setting them up is for the person's voice operator.")
	}
	switch strings.TrimSpace(input.Action) {
	case "list":
		routes, err := d.Registry.ListRoutes()
		if err != nil {
			return nil, err
		}
		return map[string]any{"relays": routes}, nil
	case "add":
		from, err := d.mcpResolveSession(input.From)
		if err != nil {
			return nil, err
		}
		to, err := d.mcpResolveSession(input.To)
		if err != nil {
			return nil, err
		}
		return d.mcpAction(ctx, caller, "agenthail_relay", input.mcpWrite, dashboardActionRequest{Action: "relay-add", FromID: from.ID, ToID: to.ID, Pattern: input.Pattern, Once: input.Once, Handoff: strings.TrimSpace(input.When)})
	case "remove":
		if input.RelayID <= 0 {
			return nil, refuse("invalid", "relayId is required to remove a relay.")
		}
		return d.mcpAction(ctx, caller, "agenthail_relay", input.mcpWrite, dashboardActionRequest{Action: "relay-remove", RelayID: input.RelayID})
	}
	return nil, refuse("invalid", "action is add, remove or list.")
}
