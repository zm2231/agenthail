package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
)

func (a *App) sourceSessionID(ctx context.Context, selector string) (string, error) {
	if selector == "" {
		if id := os.Getenv("AGENTHAIL_SESSION_ID"); id != "" {
			selector = id
		} else if id := os.Getenv("CODEX_THREAD_ID"); id != "" {
			selector = "codex:" + id
		} else if id := os.Getenv("CLAUDE_SESSION_ID"); id != "" {
			selector = "claude:" + id
		}
	}
	if selector == "" {
		return "", nil
	}
	if a.Registry != nil {
		registrySelector := strings.TrimPrefix(selector, "@")
		expectedSurface := ""
		if kind, target, qualified := strings.Cut(registrySelector, ":"); qualified {
			expectedSurface = strings.ToLower(kind)
			registrySelector = strings.TrimPrefix(target, "@")
		}
		if id, err := a.Registry.ResolveTarget(registrySelector); err == nil {
			kind, _, _, sessionErr := a.Registry.GetSession(id)
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
