package daemon

import "context"

type mcpPRInput struct {
	mcpWrite
	Action  string `json:"action" jsonschema:"link, unlink, watch or unwatch."`
	PR      string `json:"pr" jsonschema:"A pull request URL, or its number."`
	Repo    string `json:"repo,omitempty" jsonschema:"owner/name, for a number outside your folder's repository."`
	Session string `json:"session,omitempty" jsonschema:"A session you spawned or lead; default yourself."`
}

func (d *Daemon) mcpPR(ctx context.Context, caller *mcpCaller, input mcpPRInput) (any, error) {
	target := caller.session.ID
	if input.Session != "" {
		controlled, err := d.mcpControlTarget(caller, input.Session, false)
		if err != nil {
			return nil, err
		}
		target = controlled.ID
	}
	return d.mcpAction(ctx, caller, "agenthail_pr", input.mcpWrite, dashboardActionRequest{Action: "pr", SessionID: target, Op: input.Action, PR: input.PR, Repo: input.Repo})
}
