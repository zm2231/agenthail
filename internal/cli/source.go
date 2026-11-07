package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

// sourceSessionID names the calling session: --from, else the conversation id
// the agent's own process puts in every tool command's environment, read at
// call time so a Claude /branch or /resume is attributed to the conversation
// that is running now.
func (a *App) sourceSessionID(ctx context.Context, selector string) (string, error) {
	var envSet []string
	if selector == "" {
		for _, source := range surface.CallerIdentity {
			if id := os.Getenv(source.Variable); id != "" {
				if len(envSet) == 0 {
					selector = id
					if source.Surface != "" {
						selector = source.Surface + ":" + id
					}
				}
				envSet = append(envSet, source.Variable)
			}
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
	if err != nil && len(envSet) > 0 {
		registryPath := "unavailable"
		if store := a.registry(); store != nil {
			registryPath = store.Path()
		}
		return "", fmt.Errorf("resolve sender: %s=%s is not a session of the Agenthail whose registry is %s; pass --from with one of its sessions, or unset %s to send without a reply address: %w", envSet[0], os.Getenv(envSet[0]), registryPath, strings.Join(envSet, " "), err)
	}
	if err != nil {
		return "", fmt.Errorf("resolve sender (use --from surface:session): %w", err)
	}
	return session.ID, nil
}
