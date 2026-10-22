package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/zm2231/agenthail/internal/agentcard"
	"github.com/zm2231/agenthail/internal/registry"
)

type claudeHookInput struct {
	SessionID string `json:"session_id"`
	Event     string `json:"hook_event_name"`
	Source    string `json:"source"`
}

// sessionContext is what the session-context hook adds to a Claude session:
// its card once per start, then one line each time the card changes. Claude
// runs the hook from every config root that has it at once, so each start
// and each change is claimed in the registry and only the claimant prints.
// Every failure prints nothing, so the hook never stands in a session's way.
func (a *App) sessionContext(input io.Reader, parent int, now time.Time) map[string]any {
	var hook claudeHookInput
	if json.NewDecoder(io.LimitReader(input, claudeHookInputLimit)).Decode(&hook) != nil || hook.SessionID == "" {
		return nil
	}
	if hook.Event != claudeStartHook.event && hook.Event != claudePromptSubmit.event {
		return nil
	}
	store, err := a.openRegistry()
	if err != nil || store == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := store.Session(hook.SessionID)
	if err != nil {
		if session, _, err = a.resolveLocalTarget(ctx, "claude:"+hook.SessionID); err != nil {
			return nil
		}
	}
	card, err := a.cardSources().For(ctx, *session)
	if err != nil {
		return nil
	}
	text := ""
	if hook.Event == claudeStartHook.event {
		claimed, err := store.ClaimSessionStart(session.ID, fmt.Sprintf("%s:%d", hook.Source, parent), card.Fingerprint(), now)
		if err == nil && claimed {
			text = agentcard.Render(card)
		}
	} else {
		change, err := store.ClaimContextChange(session.ID, card.Fingerprint(), now)
		switch {
		case err != nil:
		case change == registry.ContextNew:
			text = agentcard.Render(card)
		case change == registry.ContextChanged:
			text = agentcard.RenderChange(card)
		}
	}
	if text == "" {
		return nil
	}
	return map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": hook.Event, "additionalContext": text}}
}
