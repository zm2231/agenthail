package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (a *App) cmdGoal(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 1 {
		return fmt.Errorf("usage: agenthail goal <target> [set|edit|pause|resume|budget|clear] [value]")
	}
	ctx := context.Background()
	sess, surf, err := a.resolveTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if !surf.Capabilities().Goal {
		return fmt.Errorf("%s does not support goal management", surf.Name())
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
	if err := a.ensureWritableTarget(ctx, sess, surf); err != nil {
		return err
	}
	controller, typed := surf.(surface.GoalController)
	action := positional[1]
	switch action {
	case "clear":
		if len(positional) != 2 {
			return fmt.Errorf("usage: agenthail goal <target> clear")
		}
		return surf.GoalClear(ctx, sess)
	case "set", "edit":
		if len(positional) < 3 {
			return fmt.Errorf("usage: agenthail goal <target> %s <objective>", action)
		}
		text := strings.Join(positional[2:], " ")
		if typed {
			update := surface.GoalUpdate{Objective: &text}
			if action == "set" {
				status := surface.GoalStatusActive
				update.Status = &status
			}
			return controller.UpdateGoal(ctx, sess, update)
		}
		return surf.GoalSet(ctx, sess, text)
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
	default:
		return fmt.Errorf("unknown goal action %q", action)
	}
}

func formatGoalState(goal *surface.GoalState) string {
	var output strings.Builder
	fmt.Fprintf(&output, "%s [%s]\n", goal.Objective, goal.Status)
	if goal.Status == surface.GoalStatusBlocked || goal.Status == surface.GoalStatusUsageLimited || goal.Status == surface.GoalStatusBudgetLimited {
		output.WriteString("Needs you\n")
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
