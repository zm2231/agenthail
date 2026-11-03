package daemon

import "context"

type mcpProjectsInput struct{}

func (d *Daemon) mcpProjects(ctx context.Context, _ *mcpCaller, _ mcpProjectsInput) (any, error) {
	projects, err := d.projectLeads(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"projects": projects}, nil
}
