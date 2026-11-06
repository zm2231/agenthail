package surfaces

import (
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// codexTurnStop is the stop a failed app-server turn leaves, or nil for a turn
// that did not fail or that a usage limit ended; an interrupted or cancelled
// turn is not a stop.
func codexTurnStop(entry map[string]any) *surface.TurnStop {
	value := entry["status"]
	if object, ok := value.(map[string]any); ok {
		value = object["type"]
	}
	status, _ := value.(string)
	switch codexErrorName(status) {
	case "failed", "error", "systemerror":
	default:
		return nil
	}
	return codexFailureStop(str(entry, "id"), codexUnixTime(entry["completedAt"]), entry["error"])
}

// codexFailureStop reads a turn's error, which the app-server keys
// codexErrorInfo and the rollout keys codex_error_info, with the kind as a
// bare string or as an object keyed by the kind.
func codexFailureStop(turnID string, at time.Time, failure any) *surface.TurnStop {
	message, kind := "", ""
	switch failure := failure.(type) {
	case map[string]any:
		message = str(failure, "message")
		info := failure["codexErrorInfo"]
		if info == nil {
			info = failure["codex_error_info"]
		}
		switch info := info.(type) {
		case string:
			kind = info
		case map[string]any:
			for name := range info {
				kind = name
			}
		}
	case string:
		message = failure
	}
	if codexErrorName(kind) == "usagelimitexceeded" {
		return nil
	}
	if message = strings.TrimSpace(message); message == "" {
		message = "Codex ended the turn with an error and no message."
	}
	return &surface.TurnStop{TurnID: turnID, At: at, Reason: "Codex error: " + message}
}

func codexErrorName(value string) string {
	return strings.ToLower(strings.ReplaceAll(value, "_", ""))
}
