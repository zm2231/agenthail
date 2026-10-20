package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (a *App) cmdRoots(args []string) error {
	if len(stripFlags(args)) != 0 {
		return fmt.Errorf("usage: agenthail roots [--json]")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	roots, err := surface.DescribeClaudeConfigRoots(home)
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"claude": roots})
	}
	now := time.Now()
	for _, root := range roots {
		name := root.Name
		if root.Primary {
			name += " (primary)"
		}
		fmt.Printf("%s  %s\n  %s\n", name, root.Root, describeClaudeConfigRoot(root, now))
	}
	return nil
}

func describeClaudeConfigRoot(root surface.ClaudeConfigRoot, now time.Time) string {
	if root.Error != "" {
		return "unreadable: " + root.Error
	}
	if root.SignedInAs == nil {
		return "signed out"
	}
	login := root.SignedInAs
	parts := []string{"currently signed in as " + login.Email}
	if detail := strings.Join(nonEmpty(login.Organization, login.Plan), ", "); detail != "" {
		parts[0] += " (" + detail + ")"
	}
	if root.Usage != nil {
		if until, out := root.Usage.Exhausted(now); out {
			parts = append(parts, "out of usage until "+until.Local().Format("Jan 2 15:04"))
		}
		var limits []string
		for _, limit := range root.Usage.Limits {
			label := limit.Kind
			if limit.Scope != "" {
				label += " " + limit.Scope
			}
			limits = append(limits, fmt.Sprintf("%s %.0f%%", label, limit.Percent))
		}
		if len(limits) > 0 {
			parts = append(parts, "usage "+strings.Join(limits, ", ")+" as of "+root.Usage.FetchedAt.Local().Format("Jan 2 15:04"))
		}
	}
	return strings.Join(parts, "; ")
}

func nonEmpty(values ...string) []string {
	var out []string
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
