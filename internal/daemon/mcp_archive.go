package daemon

import "context"

type mcpArchiveInput struct {
	mcpWrite
	Target  string `json:"target" jsonschema:"A session id or @alias you spawned or, as coordinator, lead."`
	Restore bool   `json:"restore,omitempty" jsonschema:"Bring an archived session back instead."`
}

func (d *Daemon) mcpArchive(ctx context.Context, caller *mcpCaller, input mcpArchiveInput) (any, error) {
	target, err := d.mcpControlTarget(ctx, caller, input.Target, false)
	if err != nil {
		return nil, err
	}
	action := "session-archive"
	if input.Restore {
		action = "session-unarchive"
	}
	return d.mcpAction(ctx, caller, "agenthail_archive", input.mcpWrite, dashboardActionRequest{Action: action, SessionID: target.ID})
}
