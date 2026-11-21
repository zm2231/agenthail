package daemon

import "context"

type mcpArchiveInput struct {
	mcpWrite
	Target  string `json:"target" jsonschema:"A session id or @alias you spawned or, as coordinator, lead."`
	Restore bool   `json:"restore,omitempty" jsonschema:"Bring an archived session back instead."`
	mcpAskPerson
}

func (d *Daemon) mcpArchive(ctx context.Context, caller *mcpCaller, input mcpArchiveInput) (any, error) {
	action := "session-archive"
	if input.Restore {
		action = "session-unarchive"
	}
	target, err := d.mcpControlTarget(ctx, caller, input.Target)
	if err != nil {
		return d.mcpAskOnRefusal(ctx, caller, err, input.mcpAskPerson, dashboardActionRequest{Action: action})
	}
	return d.mcpAction(ctx, caller, "agenthail_archive", input.mcpWrite, dashboardActionRequest{Action: action, SessionID: target.ID})
}
