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
	envVar := ""
	if selector == "" {
		if id := os.Getenv("AGENTHAIL_SESSION_ID"); id != "" {
			selector, envVar = id, "AGENTHAIL_SESSION_ID"
		} else if id := os.Getenv("CLAUDE_CODE_SESSION_ID"); id != "" {
			selector, envVar = "claude:"+id, "CLAUDE_CODE_SESSION_ID"
		} else if id := os.Getenv("CODEX_THREAD_ID"); id != "" {
			selector, envVar = "codex:"+id, "CODEX_THREAD_ID"
		}
	}
	if selector == "" {
		return "", nil
	}
	if a.registry() != nil {
		registrySelector := strings.TrimPrefix(selector, "@")
		expectedSurface := ""
		if kind, target, qualified := strings.Cut(registrySelector, ":"); qualified {
			expectedSurface = strings.ToLower(kind)
			registrySelector = strings.TrimPrefix(target, "@")
		}
		if id, err := a.registry().ResolveTarget(registrySelector); err == nil {
			kind, _, _, sessionErr := a.registry().GetSession(id)
			if sessionErr == nil && (expectedSurface == "" || expectedSurface == strings.ToLower(kind)) {
				return id, nil
			}
		}
	}
	session, _, err := a.resolveTarget(ctx, selector)
	if err != nil && envVar != "" {
		return "", fmt.Errorf("resolve sender: %s=%s is not a session of the Agenthail whose registry is %s; pass --from with one of its sessions, or unset %s to send without a reply address: %w", envVar, os.Getenv(envVar), a.registry().Path(), envVar, err)
	}
	if err != nil {
		return "", fmt.Errorf("resolve sender (use --from surface:session): %w", err)
	}
	return session.ID, nil
}
