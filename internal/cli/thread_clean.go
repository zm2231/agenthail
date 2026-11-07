package cli

import (
	"fmt"
)

const threadCleanUsage = `usage: agenthail thread clean <target> [--force-ignored] [--json]`

type threadCleanAnswer struct {
	OK       bool `json:"ok"`
	Worktree struct {
		Path          string   `json:"path"`
		Branch        string   `json:"branch"`
		Cleanup       string   `json:"cleanup"`
		CleanupReason string   `json:"cleanupReason"`
		KeptPaths     []string `json:"keptPaths,omitempty"`
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
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	var answer threadCleanAnswer
	request := map[string]any{"action": "work-clean", "sessionId": id}
	if hasFlag(args, "--force-ignored") {
		request["forceIgnored"] = true
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
		fmt.Printf("kept %s: %s\n", answer.Worktree.Path, cleanupReasonText(answer.Worktree.CleanupReason))
		for _, path := range answer.Worktree.KeptPaths {
			fmt.Printf("  %s\n", path)
		}
		if answer.Worktree.CleanupReason == "ignored" {
			fmt.Println("run again with --force-ignored to remove it anyway")
		}
	default:
		fmt.Printf("%s: cleanup %s\n", answer.Worktree.Path, answer.Worktree.Cleanup)
	}
	return nil
}

func cleanupReasonText(reason string) string {
	switch reason {
	case "dirty":
		return "it has changes that are not committed"
	case "ignored":
		return "it holds ignored files outside build and package folders"
	case "unpushed":
		return "its branch has commits that are on no remote and not merged"
	case "running":
		return "its session is still working; it is cleaned once the turn ends"
	}
	return reason
}
