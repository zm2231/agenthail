package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (a *App) cmdRoots(args []string) error {
	if positional := stripFlags(args); len(positional) > 0 && positional[0] == "policy" {
		return a.cmdRootsPolicy(args)
	}
	if len(stripFlags(args)) != 0 {
		return fmt.Errorf("usage: agenthail roots [--json] | roots policy")
	}
	roots, err := a.claudeConfigRoots(context.Background())
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return emit(map[string]any{"claude": roots})
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

// claudeConfigRoots asks the daemon, the process that reads the sign-in
// profiles, for each root's sign-in profile and usage. Without its answer only
// the roots' logins are known.
func (a *App) claudeConfigRoots(ctx context.Context) ([]surface.ClaudeConfigRoot, error) {
	problem := "the Agenthail daemon is not running"
	if a.daemonIsRunning() {
		var listing struct {
			Claude []surface.ClaudeConfigRoot `json:"claude"`
		}
		callCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
		err := a.signinCall(callCtx, http.MethodGet, "/api/v1/claude/roots", nil, nil, &listing)
		cancel()
		if err == nil {
			return listing.Claude, nil
		}
		problem = "the Agenthail daemon did not answer: " + err.Error()
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	roots, err := surface.DescribeClaudeConfigRoots(home)
	for i := range roots {
		if roots[i].SignedInAs != nil {
			roots[i].SigninProblem = problem
		}
	}
	return roots, err
}

func describeClaudeConfigRoot(root surface.ClaudeConfigRoot, now time.Time) string {
	if root.Error != "" {
		return "unreadable: " + root.Error
	}
	if root.Proxy != "" {
		return "via the " + root.Proxy + " proxy; agenthail proxies shows its accounts"
	}
	if root.SignedInAs == nil {
		return "signed out"
	}
	login := root.SignedInAs
	parts := []string{"currently signed in as " + login.Email}
	if detail := strings.Join(nonEmpty(login.Organization, login.Plan), ", "); detail != "" {
		parts[0] += " (" + detail + ")"
	}
	if root.Signin != "" {
		parts = append(parts, "sign-in "+root.Signin)
	}
	if root.SigninProblem != "" {
		parts = append(parts, "no sign-in: "+root.SigninProblem)
	}
	if root.UsageProblem != "" {
		parts = append(parts, "usage unavailable: "+root.UsageProblem)
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
