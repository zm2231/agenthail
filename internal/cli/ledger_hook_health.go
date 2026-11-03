package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zm2231/agenthail/internal/claudehooks"
	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

var (
	resolveSessionProject = daemon.ResolvePathProject
	localSessionPlaces    = (*registry.Registry).LocalSessionPlaces
)

type promptHookProblem struct {
	Root  string `json:"root"`
	State string `json:"state"`
	// Set when the root holds sessions of a project with the ledger explicitly enabled.
	EnabledProject string `json:"enabledProject,omitempty"`
}

func (p promptHookProblem) String() string {
	text := fmt.Sprintf("prompt hook %s in %s: terminal words are not captured live; run agenthail hooks install", p.State, p.Root)
	if p.EnabledProject != "" {
		text += fmt.Sprintf(" (the ledger is enabled for %s, whose sessions use this root)", p.EnabledProject)
	}
	return text
}

func promptHookProblems(roots []string) []promptHookProblem {
	var problems []promptHookProblem
	for _, root := range roots {
		state, err := claudehooks.StateAt(filepath.Join(root, "settings.json"), claudehooks.PromptSubmit)
		if err != nil {
			state = claudehooks.Missing
		}
		if state != claudehooks.Installed {
			problems = append(problems, promptHookProblem{Root: root, State: state})
		}
	}
	return problems
}

func ledgerPromptHookProblems(ctx context.Context, r *registry.Registry) ([]promptHookProblem, error) {
	enabled, err := ledger.NewStore(r.WriteDB(), r.ReadDB()).EnabledProjects()
	if err != nil {
		return nil, err
	}
	if len(enabled) == 0 {
		coordinators, err := r.LocalCoordinators()
		if err != nil {
			return nil, err
		}
		if len(coordinators) == 0 {
			return nil, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	roots, err := surface.DescribeClaudeConfigRoots(home)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(roots))
	for _, root := range roots {
		paths = append(paths, root.Root)
	}
	problems := promptHookProblems(paths)
	if len(problems) == 0 || len(enabled) == 0 {
		return problems, nil
	}
	affected := map[string]bool{}
	for _, problem := range problems {
		affected[problem.Root] = true
	}
	held, unresolved, err := enabledProjectsByRoot(ctx, r, roots, affected, enabled)
	if err != nil {
		return problems, err
	}
	for i := range problems {
		problems[i].EnabledProject = held[problems[i].Root]
	}
	return problems, errors.Join(unresolved...)
}

func enabledProjectsByRoot(ctx context.Context, r *registry.Registry, roots []surface.ClaudeConfigRoot, affected map[string]bool, enabled map[string]bool) (map[string]string, []error, error) {
	places, err := localSessionPlaces(r, ctx, surface.KindClaude)
	if err != nil {
		return nil, nil, err
	}
	keys := map[string]string{}
	resolveErrs := map[string]error{}
	held := map[string]string{}
	unknown := map[string][]error{}
	stopped := func(err error) []error {
		return []error{fmt.Errorf("stopped telling which projects use the Claude config roots: %w", err)}
	}
	for _, place := range places {
		if err := ctx.Err(); err != nil {
			return held, stopped(err), nil
		}
		root := surface.ClaudeSessionConfigRoot(roots, place.Transcript)
		if root == nil || !affected[root.Root] || held[root.Root] != "" {
			continue
		}
		key, seen := keys[place.Cwd]
		if !seen {
			project, err := resolveSessionProject(ctx, r, place.Cwd)
			if err != nil {
				resolveErrs[place.Cwd] = fmt.Errorf("project of %s: %w", place.Cwd, err)
			} else if project != nil {
				key = project.Key
			}
			keys[place.Cwd] = key
		}
		if err := resolveErrs[place.Cwd]; err != nil {
			unknown[root.Root] = append(unknown[root.Root], err)
		}
		if enabled[key] {
			held[root.Root] = key
		}
	}
	if err := ctx.Err(); err != nil {
		return held, stopped(err), nil
	}
	var failed []error
	for root, errs := range unknown {
		if held[root] == "" {
			failed = append(failed, errs...)
		}
	}
	return held, failed, nil
}
