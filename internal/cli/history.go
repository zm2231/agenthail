package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/zm2231/agenthail/internal/registry"
)

func (a *App) cmdHistory(args []string) error {
	if a.registry() == nil {
		return fmt.Errorf("history requires the registry")
	}
	positional := stripFlags(args)
	if len(positional) > 2 {
		return fmt.Errorf("usage: agenthail history [target] [count] [--json]")
	}
	sessionID := ""
	limit := 50
	if len(positional) > 0 {
		if len(positional) == 2 {
			parsed, err := strconv.Atoi(positional[1])
			if err != nil || parsed < 1 {
				return fmt.Errorf("count must be a positive integer")
			}
			limit = parsed
		}
		sessionID = positional[0]
		if len(positional) == 1 {
			if parsed, err := strconv.Atoi(sessionID); err == nil {
				limit = parsed
				sessionID = ""
			}
		}
	}
	if limit > 200 {
		return fmt.Errorf("count must be 200 or less")
	}
	if sessionID != "" {
		ctx := context.Background()
		session, _, remote, err := a.resolveAnyTarget(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("resolve history target %q: %w", sessionID, err)
		}
		if remote != nil {
			return a.historyRemote(ctx, remote, limit, hasFlag(args, "--json"))
		}
		sessionID = session.ID
	}
	entries, err := a.registry().ListHistory(limit, sessionID)
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return emit(map[string]any{"history": entries})
	}
	if len(entries) == 0 {
		fmt.Println("(no delivery history)")
		return nil
	}
	for _, entry := range entries {
		target := entry.SessionID
		if target != "" {
			target = a.resolveDisplay(target)
		}
		if entry.SourceSessionID != "" {
			fmt.Printf("%s %-18s %s -> %s", entry.CreatedAt, historyEvidenceLabel(entry), a.resolveDisplay(entry.SourceSessionID), target)
		} else {
			fmt.Printf("%s %-18s %s", entry.CreatedAt, historyEvidenceLabel(entry), target)
		}
		fmt.Print(historyOperationField(string(entry.Operation)))
		if entry.Message != "" {
			fmt.Printf(" %s", truncate(strings.ReplaceAll(entry.Message, "\n", " "), 140))
		}
		if entry.Error != "" {
			fmt.Printf(" error=%s", truncate(strings.ReplaceAll(entry.Error, "\n", " "), 140))
		}
		fmt.Println()
	}
	return nil
}

func historyOperationField(operation string) string {
	if operation == "" || operation == string(registry.QueueOperationMessage) {
		return ""
	}
	return " operation=" + operation
}

func historyEvidenceLabel(entry registry.HistoryEntry) string {
	if entry.Evidence != "" {
		return string(entry.Evidence)
	}
	return entry.Kind
}
