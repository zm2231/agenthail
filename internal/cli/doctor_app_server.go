package cli

import "github.com/zm2231/agenthail/internal/surface"

func appServerLine(server *surface.AppServer) string {
	if server == nil {
		return ""
	}
	switch server.Mode {
	case surface.AppServerShared:
		return "shared (Codex Desktop uses Codex's app-server daemon)"
	case surface.AppServerPrivate:
		return "private (Codex Desktop runs its own app-server, separate from Codex's daemon)"
	}
	return ""
}
