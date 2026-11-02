package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const promptHookSessionsScanned = 500

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
		state, err := claudeHookState(filepath.Join(root, "settings.json"), claudePromptSubmit)
		if err != nil {
			state = claudeHookMissing
		}
		if state != claudeHookInstalled {
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
	held, err := enabledProjectsByRoot(ctx, r, roots, enabled)
	if err != nil {
		return nil, err
	}
	for i := range problems {
		problems[i].EnabledProject = held[problems[i].Root]
	}
	return problems, nil
}

func enabledProjectsByRoot(ctx context.Context, r *registry.Registry, roots []surface.ClaudeConfigRoot, enabled map[string]bool) (map[string]string, error) {
	sessions, err := r.ListSessions(promptHookSessionsScanned)
	if err != nil {
		return nil, err
	}
	keys := map[string]string{}
	held := map[string]string{}
	for _, session := range sessions {
		if session.Surface != surface.KindClaude || session.Cwd == "" {
			continue
		}
		root := surface.ClaudeSessionConfigRoot(roots, session.Transcript)
		if root == nil || held[root.Root] != "" {
			continue
		}
		key, seen := keys[session.Cwd]
		if !seen {
			if project, err := daemon.ResolvePathProject(ctx, r, session.Cwd); err == nil && project != nil {
				key = project.Key
			}
			keys[session.Cwd] = key
		}
		if enabled[key] {
			held[root.Root] = key
		}
	}
	return held, nil
}
