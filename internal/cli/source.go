package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// sourceSessionID names the calling session: --from, else the conversation id
// the agent's own process puts in every tool command's environment, read at
// call time so a Claude /branch or /resume is attributed to the conversation
// that is running now.
func (a *App) sourceSessionID(ctx context.Context, selector string) (string, error) {
	if selector == "" {
		if id := os.Getenv("AGENTHAIL_SESSION_ID"); id != "" {
			selector = id
		} else if id := os.Getenv("CLAUDE_CODE_SESSION_ID"); id != "" {
			selector = "claude:" + id
		} else if id := os.Getenv("CODEX_THREAD_ID"); id != "" {
			selector = "codex:" + id
		}
	}
	if selector == "" {
		return "", nil
	}
	if a.registry() != nil {
		registrySelector := selector
		expectedSurface := ""
		if kind, target, qualified := strings.Cut(registrySelector, ":"); qualified {
			expectedSurface = strings.ToLower(kind)
			registrySelector = target
		}
		if id, err := a.registry().ResolveTarget(registrySelector); err == nil {
			kind, _, _, sessionErr := a.registry().GetSession(id)
			if sessionErr == nil && (expectedSurface == "" || expectedSurface == strings.ToLower(kind)) {
				return id, nil
			}
		}
	}
	session, _, err := a.resolveTarget(ctx, selector)
	if err != nil {
		return "", fmt.Errorf("resolve sender (use --from surface:session): %w", err)
	}
	return session.ID, nil
}
