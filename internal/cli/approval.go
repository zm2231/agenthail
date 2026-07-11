package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/approval"
)

const maxApprovalHookBytes = 128 << 10

func (a *App) cmdApproval(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: agenthail approval <hook|list|allow|deny> ...")
	}
	store, err := approval.Open(approval.DefaultPath())
	if err != nil {
		if args[0] == "hook" {
			return writeApprovalHookFailure(fmt.Errorf("approval broker unavailable: %w", err))
		}
		return err
	}
	defer store.Close()
	switch args[0] {
	case "hook":
		if len(args) != 1 {
			return fmt.Errorf("usage: agenthail approval hook < request.json")
		}
		return runApprovalHook(store)
	case "list":
		return listApprovals(store, args[1:])
	case "allow", "approve":
		return decideApproval(store, approval.DecisionAllow, args[1:])
	case "deny", "reject":
		return decideApproval(store, approval.DecisionDeny, args[1:])
	default:
		return fmt.Errorf("unknown approval command %q (use hook, list, allow, or deny)", args[0])
	}
}

func runApprovalHook(store *approval.Store) error {
	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxApprovalHookBytes+1))
	if err != nil {
		return writeApprovalHookFailure(fmt.Errorf("read approval request: %w", err))
	}
	if len(data) > maxApprovalHookBytes {
		return writeApprovalHookFailure(fmt.Errorf("approval request exceeds %d bytes", maxApprovalHookBytes))
	}
	request, err := approval.Normalize(data, time.Now())
	if err != nil {
		return writeApprovalHookFailure(err)
	}
	response, _, err := store.Observe(request, time.Now())
	if err != nil {
		return writeApprovalHookFailure(fmt.Errorf("approval broker unavailable: %w", err))
	}
	return writeApprovalHookResponse(response)
}

func writeApprovalHookFailure(err error) error {
	response := approval.Response{Decision: approval.DecisionDeny, Reason: "approval denied: " + err.Error()}
	_ = writeApprovalHookResponse(response)
	return err
}

func writeApprovalHookResponse(response approval.Response) error {
	decision := response.Decision
	if decision != approval.DecisionAllow {
		decision = approval.DecisionDeny
	}
	output := map[string]any{
		"requestId": response.RequestID,
		"decision":  decision,
		"reason":    response.Reason,
		"hookSpecificOutput": map[string]any{
			"hookEventName": "PermissionRequest",
			"decision": map[string]any{
				"behavior": string(decision),
				"message":  response.Reason,
			},
		},
	}
	return json.NewEncoder(os.Stdout).Encode(output)
}

func listApprovals(store *approval.Store, args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("usage: agenthail approval list [count] [--json]")
	}
	limit := 50
	for _, arg := range args {
		if strings.HasPrefix(arg, "--") {
			continue
		}
		parsed, err := strconv.Atoi(arg)
		if err != nil || parsed < 1 {
			return fmt.Errorf("count must be a positive integer")
		}
		limit = parsed
	}
	rows, err := store.List(limit)
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"approvals": rows})
	}
	if len(rows) == 0 {
		fmt.Println("(no approval requests)")
		return nil
	}
	for _, row := range rows {
		fmt.Printf("%s %-7s %-16s %s", row.ID, row.Decision, row.Tool, row.Summary)
		if row.SessionID != "" {
			fmt.Printf(" session=%s", row.SessionID)
		}
		if row.DecisionReason != "" {
			fmt.Printf(" reason=%s", row.DecisionReason)
		}
		fmt.Println()
	}
	return nil
}

func decideApproval(store *approval.Store, decision approval.Decision, args []string) error {
	if len(args) < 1 || len(args) > 2 || strings.HasPrefix(args[0], "--") {
		return fmt.Errorf("usage: agenthail approval %s <request-id> [reason]", decision)
	}
	reason := "approved locally"
	if decision == approval.DecisionDeny {
		reason = "denied locally"
	}
	if len(args) == 2 {
		reason = strings.TrimSpace(args[1])
		if reason == "" {
			return fmt.Errorf("approval reason is empty")
		}
	}
	if err := store.Decide(args[0], decision, reason, time.Now()); err != nil {
		return err
	}
	fmt.Printf("approval %s: %s\n", decision, args[0])
	return nil
}
