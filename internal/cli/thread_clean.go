package cli

import (
	"fmt"
)

const threadCleanUsage = `usage: agenthail thread clean <target> [--remove-ignored <path>]... [--json]`

type threadCleanAnswer struct {
	OK       bool `json:"ok"`
	Worktree struct {
		Path          string   `json:"path"`
		Branch        string   `json:"branch"`
		Cleanup       string   `json:"cleanup"`
		CleanupReason string   `json:"cleanupReason"`
		KeptPaths     []string `json:"keptPaths,omitempty"`
		Hint          *struct {
			Reason  string `json:"reason"`
			Command string `json:"command"`
		} `json:"hint,omitempty"`
	} `json:"worktree"`
}

func (a *App) cmdThreadClean(args []string) error {
	positional := stripFlags(args)
	if len(positional) != 2 {
		return fmt.Errorf("%s", threadCleanUsage)
	}
	ctx, cancel, err := a.deadlineContext(a.DefaultTimeout)
	if err != nil {
		return err
	}
	defer cancel()
	session, _, remote, err := a.resolveAnyTarget(ctx, positional[1])
	if err != nil {
		return err
	}
	var id string
	if remote != nil {
		id = remote.ID
	} else {
		id = session.ID
	}
	if err := a.controlScopeOf(ctx, id); err != nil {
		return err
	}
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	var answer threadCleanAnswer
	request := map[string]any{"action": "work-clean", "sessionId": id}
	if paths := flagVals(args, "--remove-ignored"); len(paths) > 0 {
		request["removeIgnored"] = paths
	}
	if err := projectAction(ctx, client, request, &answer); err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return emit(answer)
	}
	switch answer.Worktree.Cleanup {
	case "removed":
		fmt.Printf("removed %s and branch %s\n", answer.Worktree.Path, answer.Worktree.Branch)
	case "kept":
		reason := answer.Worktree.CleanupReason
		if hint := answer.Worktree.Hint; hint != nil {
			reason = hint.Reason
		}
		fmt.Printf("kept %s: %s\n", answer.Worktree.Path, reason)
		for _, path := range answer.Worktree.KeptPaths {
			fmt.Printf("  %s\n", path)
		}
		if hint := answer.Worktree.Hint; hint != nil && hint.Command != "" {
			fmt.Printf("see it with: %s\n", hint.Command)
		}
		if answer.Worktree.CleanupReason == "ignored" {
			fmt.Println("to delete exactly these files with it, run again naming each with --remove-ignored <path>")
		}
	default:
		fmt.Printf("%s: cleanup %s\n", answer.Worktree.Path, answer.Worktree.Cleanup)
	}
	return nil
}
