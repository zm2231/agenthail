package cli

import (
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/agenttools"
	"github.com/zm2231/agenthail/internal/registry"
)

type stopHookInput struct {
	SessionID            string `json:"session_id"`
	Event                string `json:"hook_event_name"`
	StopHookActive       bool   `json:"stop_hook_active"`
	AgentID              string `json:"agent_id"`
	TurnID               string `json:"turn_id"`
	LastAssistantMessage string `json:"last_assistant_message"`
}

const (
	turnStopNudge     = "[agenthail] This turn has no end yet. Call " + agenttools.EndTurn + " now with a one-line headline. Don't repeat your reply."
	askReminderTools  = "[agenthail] Your reply asks the person something. Raise it with " + agenttools.Ask + " so it reaches Needs you, then stop."
	askReminderTyped  = "[agenthail] Your reply asks the person something. Raise it with agenthail ask \"<title>\" --option <label> --option <label> so it reaches Needs you, then stop."
	maxSpokenQuestion = 8 << 10
)

// turnStop holds a turn's stop once, so a session holding Agenthail's tools
// ends its turn with them and a lead that asks the person in prose raises an
// ask. A stop that follows a hold or a subagent's stop is let through, and
// every failure lets the stop through. The Claude host skips the hook itself
// while background work runs, since only it knows which tasks are ambient.
func (a *App) turnStop(input io.Reader, now time.Time) map[string]any {
	var hook stopHookInput
	if json.NewDecoder(io.LimitReader(input, claudeHookInputLimit)).Decode(&hook) != nil || hook.SessionID == "" || hook.Event != "Stop" {
		return nil
	}
	if hook.StopHookActive || hook.AgentID != "" {
		return nil
	}
	_, cancel, err := a.deadlineContext(claudeSessionContextLimit)
	if err != nil {
		return nil
	}
	defer cancel()
	store, err := a.openRegistry()
	if err != nil || store == nil {
		return nil
	}
	session, err := store.Session(hook.SessionID)
	if err != nil {
		return nil
	}
	_, tools, err := store.LiveCredentialHash(session.ID, now)
	if err != nil {
		return nil
	}
	var reasons []string
	if tools {
		if ended, err := store.TurnEnded(session.ID, hook.TurnID); err == nil && !ended {
			reasons = append(reasons, turnStopNudge)
		}
	}
	if asksInProse(hook.LastAssistantMessage) && leads(store, session.ID) && !raisedThisTurn(store, session.ID) {
		if tools {
			reasons = append(reasons, askReminderTools)
		} else {
			reasons = append(reasons, askReminderTyped)
		}
	}
	if len(reasons) == 0 {
		return nil
	}
	if reasons[0] == turnStopNudge && store.MarkTurnNudged(session.ID, now) != nil {
		return nil
	}
	return map[string]any{"decision": "block", "reason": strings.Join(reasons, "\n")}
}

// A lead is a project's coordinator or the spawner of a session still running: the sessions whose questions would otherwise reach no one.
func leads(store *registry.Registry, sessionID string) bool {
	if _, coordinates, err := store.CoordinatorOfSession(sessionID); err == nil && coordinates {
		return true
	}
	children, err := store.SessionChildren(sessionID)
	return err == nil && len(children) > 0
}

func raisedThisTurn(store *registry.Registry, sessionID string) bool {
	started, found, err := store.TurnStartedAt(sessionID)
	if err != nil || !found {
		return true
	}
	raised, err := store.AskRaisedSince(sessionID, started)
	return err != nil || raised
}

// asksInProse is a fixed text rule: a line outside fenced code that ends in a question mark.
func asksInProse(reply string) bool {
	if len(reply) > maxSpokenQuestion {
		reply = reply[len(reply)-maxSpokenQuestion:]
	}
	fenced := false
	for line := range strings.Lines(reply) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			fenced = !fenced
			continue
		}
		if !fenced && strings.HasSuffix(strings.TrimRight(line, "*_)\"'` "), "?") {
			return true
		}
	}
	return false
}
