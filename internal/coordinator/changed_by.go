package coordinator

import (
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

// ChangedByLabel names who replaced a coordinator, using display for a session.
func ChangedByLabel(changedBy string, display func(string) string) string {
	switch changedBy {
	case surface.PersonSessionID:
		return "the person"
	case surface.CLISessionID, "":
		return "the CLI"
	case RestartedByDaemon:
		return "the daemon"
	}
	return display(changedBy)
}

// ChangedByAgent reports whether changedBy is an agent's session rather than the person, the CLI or the daemon.
func ChangedByAgent(changedBy string) bool {
	switch changedBy {
	case "", surface.PersonSessionID, surface.CLISessionID, RestartedByDaemon:
		return false
	}
	return true
}

// ReplacementWhy is a handover's reason without its "gone: " tag.
func ReplacementWhy(reason string) string {
	why, _ := strings.CutPrefix(reason, "gone: ")
	return why
}
