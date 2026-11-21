package cli

import (
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

func appServerLine(server *surface.AppServer) string {
	if server == nil {
		return ""
	}
	var parts []string
	switch server.Mode {
	case surface.AppServerShared:
		parts = append(parts, "shared (Codex Desktop uses Codex's app-server daemon)")
	case surface.AppServerPrivate:
		parts = append(parts, "private (Codex Desktop runs its own app-server, separate from Codex's daemon)")
	}
	if server.Version != "" {
		parts = append(parts, "version "+server.Version)
	}
	if server.Transport != "" {
		parts = append(parts, "over "+server.Transport)
	}
	if server.HostID != "" {
		parts = append(parts, "host "+server.HostID)
	}
	return strings.Join(parts, ", ")
}
