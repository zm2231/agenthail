package daemon

import "context"

var mcpLimits = map[string]int{"depth": 3, "children": 8, "outputBytes": mcpOutputLimit, "itemBytes": mcpItemTextLimit}

func (d *Daemon) mcpCapabilities(_ context.Context, caller *mcpCaller, _ struct{}) (any, error) {
	surfaces := []map[string]any{}
	for _, adapter := range d.Surfaces {
		surfaces = append(surfaces, map[string]any{"surface": string(adapter.Name()), "spawn": false, "reason": "This server offers read tools only."})
	}
	reach := []string{}
	for _, grant := range []struct{ capability, scope string }{{"read.self", "your own session"}, {"read.project", "your project's sessions"}, {"control.descendants", "sessions you spawned"}} {
		if caller.has(grant.capability) {
			reach = append(reach, grant.scope)
		}
	}
	return map[string]any{
		"capabilities": caller.capabilities,
		"ceiling":      caller.ceiling,
		"reads":        reach,
		"surfaces":     surfaces,
		"limits":       mcpLimits,
	}, nil
}
