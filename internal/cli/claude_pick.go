package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/zm2231/agenthail/internal/claudepick"
	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/surface"
)

const rootsPolicyUsage = "usage: agenthail roots policy [--json] | auto on|off | prefer <root>|none | ceilings <session %> <week %>"

func (a *App) claudeRootPick(ctx context.Context) (claudepick.Pick, error) {
	if a.daemonIsRunning() {
		var answer struct {
			Pick claudepick.Pick `json:"pick"`
		}
		callCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
		err := a.signinCall(callCtx, http.MethodGet, "/api/v1/session-options/claude-pick", nil, nil, &answer)
		cancel()
		if err == nil {
			return answer.Pick, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return claudepick.Pick{}, err
	}
	roots, err := surface.DescribeClaudeConfigRoots(home)
	if err != nil {
		return claudepick.Pick{}, err
	}
	config, err := daemon.LoadDashboardConfig()
	if err != nil {
		return claudepick.Pick{}, err
	}
	candidates := make([]claudepick.Candidate, 0, len(roots))
	for _, root := range roots {
		candidate := claudepick.Candidate{Name: root.Name, Root: root.Root, Primary: root.Primary, Login: surface.ClaudeRootLogin(home, root)}
		if root.SignedInAs != nil {
			candidate.Account = root.SignedInAs.Email
		}
		candidates = append(candidates, candidate)
	}
	return claudepick.Choose(candidates, config.ClaudeAccountPolicy(), time.Now()), nil
}

func (a *App) cmdRootsPolicy(args []string) error {
	positional := stripFlags(args)[1:]
	config, err := daemon.LoadDashboardConfig()
	if err != nil {
		return err
	}
	policy := config.ClaudeAccountPolicy()
	switch {
	case len(positional) == 0:
		pick, err := a.claudeRootPick(context.Background())
		if err != nil {
			return err
		}
		if hasFlag(args, "--json") {
			return emit(map[string]any{"policy": policy, "pick": pick})
		}
		auto := "on"
		if !policy.Auto {
			auto = "off"
		}
		preferred := policy.Preferred
		if preferred == "" {
			preferred = "none"
		}
		fmt.Printf("automatic choice: %s\npreferred root: %s\nceilings: session %.0f%%, week %.0f%%\nnext session: %s (%s)\n", auto, preferred, policy.SessionCeiling, policy.WeeklyCeiling, pickTitle(pick), pick.Reason)
		return nil
	case len(positional) == 2 && positional[0] == "auto" && (positional[1] == "on" || positional[1] == "off"):
		policy.Auto = positional[1] == "on"
	case len(positional) == 2 && positional[0] == "prefer":
		policy.Preferred = positional[1]
		if policy.Preferred == "none" {
			policy.Preferred = ""
		} else if err := knownRoot(policy.Preferred); err != nil {
			return err
		}
	case len(positional) == 3 && positional[0] == "ceilings":
		session, sessionErr := strconv.ParseFloat(positional[1], 64)
		weekly, weeklyErr := strconv.ParseFloat(positional[2], 64)
		if sessionErr != nil || weeklyErr != nil {
			return errors.New(rootsPolicyUsage)
		}
		policy.SessionCeiling, policy.WeeklyCeiling = session, weekly
	default:
		return errors.New(rootsPolicyUsage)
	}
	if err := config.SetClaudeAccountPolicy(policy); err != nil {
		return err
	}
	if err := daemon.SaveDashboardConfig(config); err != nil {
		return err
	}
	fmt.Println("saved; new Claude sessions on this host follow it")
	return nil
}

func knownRoot(name string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	roots, err := surface.DescribeClaudeConfigRoots(home)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(roots))
	for _, root := range roots {
		names = append(names, root.Name)
	}
	if !slices.Contains(names, name) {
		return fmt.Errorf("no configured Claude config root is named %q; choose one of %v", name, names)
	}
	return nil
}

func pickTitle(pick claudepick.Pick) string {
	if pick.Account != "" {
		return pick.Name + ", " + pick.Account
	}
	return pick.Name
}
