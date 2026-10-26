package daemon

import (
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// Codex starts no turn when background work ends, so a held turn completes when that work stops.
func heldCompletion(previous registry.RuntimeState, observation *surface.TurnObservation, completionChanged bool) (held string, released bool) {
	backgroundOnly := observation.ActiveTurnID == "" && surface.BackgroundRunning(observation.BackgroundTasks)
	if completionChanged {
		if backgroundOnly {
			return observation.CompletedTurnID, false
		}
		return "", false
	}
	if previous.HeldCompletionTurnID == "" || observation.ActiveTurnID != "" || observation.CompletedTurnID != previous.HeldCompletionTurnID {
		return "", false
	}
	if backgroundOnly {
		return previous.HeldCompletionTurnID, false
	}
	return "", true
}
