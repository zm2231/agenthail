package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type promptHookProblem struct {
	Root  string `json:"root"`
	State string `json:"state"`
}

func (p promptHookProblem) String() string {
	return fmt.Sprintf("prompt hook %s in %s: terminal words are not captured live; run agenthail hooks install", p.State, p.Root)
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

// The prompt hook is the live capture point once any project captures words.
func ledgerPromptHookProblems(r *registry.Registry) ([]promptHookProblem, error) {
	enabled, err := ledger.NewStore(r.WriteDB(), r.ReadDB()).AnyEnabled()
	if err != nil {
		return nil, err
	}
	if !enabled {
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
	roots, err := surface.ClaudeConfigRoots(home)
	if err != nil {
		return nil, err
	}
	return promptHookProblems(roots), nil
}
