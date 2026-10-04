package daemon

import (
	"context"
	"fmt"
	"sort"

	"github.com/zm2231/agenthail/internal/surface"
)

type SessionTransportResolver struct {
	launchers map[string]surface.Launcher
}

func NewSessionTransportResolver(launchers []surface.Launcher) *SessionTransportResolver {
	result := &SessionTransportResolver{launchers: map[string]surface.Launcher{}}
	for _, launcher := range launchers {
		if launcher != nil {
			result.launchers[launcher.ID()] = launcher
		}
	}
	return result
}

func (r *SessionTransportResolver) Launcher(id string) (surface.Launcher, bool) {
	launcher, ok := r.launchers[id]
	return launcher, ok
}

func (r *SessionTransportResolver) Options(ctx context.Context) []map[string]any {
	result := make([]map[string]any, 0, len(r.launchers))
	for id, launcher := range r.launchers {
		available, detail := launcher.Available(ctx)
		agents := launcher.Agents()
		result = append(result, map[string]any{"id": id, "label": string(id), "agents": agents, "available": available, "detail": detail})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i]["id"].(string) < result[j]["id"].(string)
	})
	return result
}

func (r *SessionTransportResolver) Require(id string, agent surface.SurfaceKind) (surface.Launcher, error) {
	launcher, ok := r.Launcher(id)
	if !ok {
		return nil, fmt.Errorf("launcher %q is unavailable", id)
	}
	for _, supported := range launcher.Agents() {
		if supported == agent {
			return launcher, nil
		}
	}
	return nil, fmt.Errorf("launcher %q does not support %s", id, agent)
}

func defaultLauncherForSurface(kind surface.SurfaceKind) string {
	switch kind {
	case surface.KindClaude:
		return surface.LauncherClaudeBG
	case surface.KindCodex:
		return surface.LauncherCodexAppServer
	default:
		return surface.LauncherExternal
	}
}

func locationMatches(expected, actual surface.Location) bool {
	if expected.Workspace != "" && expected.Workspace != actual.Workspace {
		return false
	}
	if expected.Surface != "" && expected.Surface != actual.Surface {
		return false
	}
	if expected.Session != "" && expected.Session != actual.Session {
		return false
	}
	if expected.Pane != "" && expected.Pane != actual.Pane {
		return false
	}
	return true
}

func locateLaunchedSession(ctx context.Context, launcher surface.Launcher, adapter surface.Surface, result surface.LaunchResult) (*surface.Session, *surface.Location, error) {
	if result.Session != nil {
		return result.Session, result.Location, nil
	}
	if result.SessionID != "" {
		return nil, result.Location, fmt.Errorf("launcher %q returned session ID without session metadata", launcher.ID())
	}
	sessions, err := adapter.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	locations := launcher.Locate(ctx, sessions)
	id := result.SessionID
	if id == "" && result.Location != nil {
		candidates := []string{}
		for candidateID, location := range locations {
			if locationMatches(*result.Location, location) {
				candidates = append(candidates, candidateID)
			}
		}
		if len(candidates) > 1 {
			return nil, nil, fmt.Errorf("launcher %q location matched multiple sessions", launcher.ID())
		}
		if len(candidates) == 1 {
			id = candidates[0]
		}
	}
	if id == "" && result.Location != nil {
		return nil, result.Location, nil
	}
	if id == "" {
		return nil, nil, fmt.Errorf("launcher %q did not correlate a discovered session", launcher.ID())
	}
	for index := range sessions {
		if sessions[index].ID == id {
			location, found := locations[id]
			if !found && result.Location != nil {
				location = *result.Location
				found = true
			}
			if !found {
				return nil, nil, fmt.Errorf("launcher %q did not locate session %q", launcher.ID(), id)
			}
			return &sessions[index], &location, nil
		}
	}
	return nil, nil, fmt.Errorf("launcher %q did not discover session %q", launcher.ID(), id)
}
