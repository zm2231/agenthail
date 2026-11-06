package cli

import (
	"encoding/json"
	"io"
	"time"

	"github.com/zm2231/agenthail/internal/agenttools"
)

type stopHookInput struct {
	SessionID      string `json:"session_id"`
	Event          string `json:"hook_event_name"`
	StopHookActive bool   `json:"stop_hook_active"`
	AgentID        string `json:"agent_id"`
	TurnID         string `json:"turn_id"`
}

const turnStopNudge = "[agenthail] This turn has no end yet. Call " + agenttools.EndTurn + " now: a one-line headline, then needs \"you\" with your questions as asks, or needs \"nothing\". Don't repeat your reply."

// turnStop asks a session holding Agenthail's tools to end its turn once. A
// stop that follows the ask or a subagent's stop is let through, and every
// failure lets the stop through. The Claude host skips the hook itself while
// background work runs, since only it knows which tasks are ambient.
func (a *App) turnStop(input io.Reader, now time.Time) map[string]any {
	var hook stopHookInput
	if json.NewDecoder(io.LimitReader(input, claudeHookInputLimit)).Decode(&hook) != nil || hook.SessionID == "" || hook.Event != "Stop" {
		return nil
	}
	if hook.StopHookActive || hook.AgentID != "" {
		return nil
	}
	store, err := a.openRegistry()
	if err != nil || store == nil {
		return nil
	}
	session, err := store.Session(hook.SessionID)
	if err != nil {
		return nil
	}
	if _, live, err := store.LiveCredentialHash(session.ID, now); err != nil || !live {
		return nil
	}
	if ended, err := store.TurnEnded(session.ID, hook.TurnID); err != nil || ended {
		return nil
	}
	if store.MarkTurnNudged(session.ID, now) != nil {
		return nil
	}
	return map[string]any{"decision": "block", "reason": turnStopNudge}
}
