package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/zm2231/agenthail/internal/agentcard"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type claudeHookInput struct {
	SessionID string `json:"session_id"`
	Event     string `json:"hook_event_name"`
	Source    string `json:"source"`
	Prompt    string `json:"prompt"`
}

// sessionContext is what the session-context hook adds to a Claude or Codex
// session: its card once per start, then one line each time the card changes.
// Copies of the hook can run at once, from several config roots or from both
// a host and the person's settings, so each start and each change is claimed
// in the registry and only the claimant prints. Every failure prints nothing,
// so the hook never stands in a session's way.
func (a *App) sessionContext(input io.Reader, kind surface.SurfaceKind, parent int, now time.Time) map[string]any {
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
		// Codex runs the hook inside the app-server that holds the thread
		// and runs it for every thread, codex exec and review threads
		// included, so a thread the registry doesn't know gets nothing
		// rather than a lookup through Codex.
		if kind != surface.KindClaude {
			return nil
		}
		if session, _, err = a.resolveLocalTarget(ctx, string(kind)+":"+hook.SessionID); err != nil {
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
	var ledgerContext string
	if hook.Event == claudePromptSubmit.event && hook.Prompt != "" && session.Cwd != "" {
		ledgerContext = a.captureTerminalUtterance(ctx, session, hook.Prompt)
	}

	if text == "" && ledgerContext == "" {
		return nil
	}
	combined := text
	if ledgerContext != "" {
		if combined != "" {
			combined += "\n\n"
		}
		combined += ledgerContext
	}
	return map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": hook.Event, "additionalContext": combined}}
}

func (a *App) captureTerminalUtterance(ctx context.Context, session *surface.Session, prompt string) string {
	var client *daemonAPIClient
	var err error
	if a.daemonAPIOverride != nil {
		client, err = a.daemonAPIOverride()
	} else {
		client, err = newDaemonAPIClient()
	}
	if err != nil {
		return ""
	}
	messageID := terminalUtteranceID(session.ID, prompt)
	body, status, err := client.call(ctx, http.MethodPost, "/api/v1/ledger/capture", nil, map[string]string{
		"sessionId": session.ID,
		"messageId": messageID,
		"prompt":    prompt,
	}, nil)
	if err != nil || status != http.StatusOK {
		return ""
	}
	var result struct {
		Captured   bool   `json:"captured"`
		ProjectKey string `json:"projectKey"`
	}
	if json.Unmarshal(body, &result) != nil || !result.Captured {
		return ""
	}
	return fmt.Sprintf("[intent ledger] utterance %s captured for project %s", messageID, result.ProjectKey)
}

func terminalUtteranceID(sessionID, prompt string) string {
	h := sha256.Sum256([]byte(sessionID + "\x00" + prompt))
	return "u-" + hex.EncodeToString(h[:12])
}
