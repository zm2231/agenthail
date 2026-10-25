package cli

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/zm2231/agenthail/internal/daemon"
)

func listProjectID(ctx context.Context, client *daemonAPIClient, selector, scope string) (string, error) {
	if scope != "archived" {
		scope = "all"
	}
	var state daemon.SessionViewSnapshot
	if err := client.getJSON(ctx, "/api/v1/snapshot", url.Values{"scope": {scope}, "limit": {"1"}}, &state); err != nil {
		return "", err
	}
	if state.View == nil {
		return "", fmt.Errorf("daemon did not return a session view; update the daemon")
	}
	matches := []string{}
	for _, project := range state.View.Projects {
		if project.ID == "" {
			continue
		}
		if project.ID == selector {
			return project.ID, nil
		}
		if strings.EqualFold(project.Name, selector) {
			matches = append(matches, project.ID)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("project %q is ambiguous: %s; use its id", selector, strings.Join(matches, ", "))
	}
	return "", fmt.Errorf("no project matched %q", selector)
}
