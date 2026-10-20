package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/surface"
)

func (a *App) cmdGoal(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 1 {
		return fmt.Errorf("usage: agenthail goal <target> [set|edit|pause|resume|budget|clear] [value]")
	}
	ctx := context.Background()
	sess, surf, remote, err := a.resolveAnyTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if remote != nil {
		return a.goalRemote(ctx, remote, positional, hasFlag(args, "--json"))
	}
	capabilities := surface.CapabilitiesFor(surf, sess)
	if !capabilities.GoalStatus {
		return fmt.Errorf("%s does not report goals", surf.Name())
	}
	if len(positional) == 1 {
		goal, getErr := surf.GoalGet(ctx, sess)
		if getErr != nil {
			return getErr
		}
		if hasFlag(args, "--json") {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"surface": sess.Surface, "session": sess.ID, "goal": goal})
		}
		if goal == nil || goal.Objective == "" {
			fmt.Println("(no active goal)")
			return nil
		}
		fmt.Print(formatGoalState(goal))
		return nil
	}
	action := positional[1]
	switch action {
	case "set", "edit", "clear":
	case "pause", "resume", "budget":
		if !capabilities.Goal {
			return fmt.Errorf("%s does not support goal %s", surf.Name(), action)
		}
	default:
		return fmt.Errorf("unknown goal action %q", action)
	}
	if err := a.ensureWritableTarget(ctx, sess, surf); err != nil {
		return err
	}
	dispatcher := delivery.Dispatcher{Registry: a.registry()}
	controller, typed := surf.(surface.GoalController)
	switch action {
	case "clear":
		if len(positional) != 2 {
			return fmt.Errorf("usage: agenthail goal <target> clear")
		}
		receipt, err := dispatcher.GoalClear(ctx, surf, sess)
		return a.printGoalReceipt(receipt, err, sess)
	case "set", "edit":
		if len(positional) < 3 {
			return fmt.Errorf("usage: agenthail goal <target> %s <objective>", action)
		}
		receipt, err := dispatcher.GoalSet(ctx, surf, sess, strings.Join(positional[2:], " "), action == "edit")
		return a.printGoalReceipt(receipt, err, sess)
	case "pause", "resume":
		if !typed || len(positional) != 2 {
			return fmt.Errorf("%s does not support typed goal status controls", surf.Name())
		}
		status := surface.GoalStatusPaused
		if action == "resume" {
			status = surface.GoalStatusActive
		}
		return controller.UpdateGoal(ctx, sess, surface.GoalUpdate{Status: &status})
	case "budget":
		if !typed || len(positional) != 3 {
			return fmt.Errorf("usage: agenthail goal <target> budget <tokens|clear>")
		}
		if positional[2] == "clear" {
			return controller.UpdateGoal(ctx, sess, surface.GoalUpdate{ClearTokenBudget: true})
		}
		budget, parseErr := strconv.ParseInt(positional[2], 10, 64)
		if parseErr != nil || budget < 0 {
			return fmt.Errorf("goal token budget must be a non-negative integer")
		}
		return controller.UpdateGoal(ctx, sess, surface.GoalUpdate{TokenBudget: &budget})
	}
	return nil
}

func (a *App) printGoalReceipt(receipt *delivery.Receipt, err error, sess *surface.Session) error {
	if err != nil {
		return err
	}
	if receipt != nil && receipt.Evidence == surface.EvidenceQueued {
		fmt.Printf("queued for %s as #%d: %s\n", a.resolveDisplay(sess.ID), receipt.QueueID, receipt.Detail)
	}
	return nil
}

func formatGoalState(goal *surface.GoalState) string {
	var output strings.Builder
	fmt.Fprintf(&output, "%s [%s]\n", goal.Objective, goal.Status)
	if goal.Status == surface.GoalStatusBlocked || goal.Status == surface.GoalStatusUsageLimited || goal.Status == surface.GoalStatusBudgetLimited {
		output.WriteString("Needs you\n")
	}
	if goal.Reason != "" {
		fmt.Fprintf(&output, "Reason: %s\n", goal.Reason)
	}
	fmt.Fprintf(&output, "Elapsed: %ds · Tokens: %d", goal.TimeUsedSeconds, goal.TokensUsed)
	if goal.TokenBudget != nil {
		fmt.Fprintf(&output, " / %d", *goal.TokenBudget)
	}
	output.WriteByte('\n')
	if !goal.CreatedAt.IsZero() {
		fmt.Fprintf(&output, "Created: %s\n", goal.CreatedAt.UTC().Format(time.RFC3339))
	}
	if !goal.UpdatedAt.IsZero() {
		fmt.Fprintf(&output, "Updated: %s\n", goal.UpdatedAt.UTC().Format(time.RFC3339))
	}
	return output.String()
}
