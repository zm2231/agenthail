package daemon

import (
	"context"
	"strings"
)

type mcpAliasInput struct {
	mcpWrite
	Target string `json:"target" jsonschema:"A session id or @alias you spawned or, as coordinator, lead."`
	Name   string `json:"name" jsonschema:"The name to reach it by, without @: 1 to 80 characters, no spaces, / or #."`
}

func (d *Daemon) mcpAlias(ctx context.Context, caller *mcpCaller, input mcpAliasInput) (any, error) {
	if strings.TrimSpace(input.Name) == "" {
		return nil, refuse("invalid", "name is required.")
	}
	target, err := d.mcpControlTarget(caller, input.Target, false)
	if err != nil {
		return nil, err
	}
	return d.mcpAction(ctx, caller, "agenthail_alias", input.mcpWrite, dashboardActionRequest{Action: "alias", SessionID: target.ID, Alias: input.Name})
}
