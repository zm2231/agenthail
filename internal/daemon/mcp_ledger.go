package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

type mcpLedgerInput struct {
	View   string `json:"view,omitempty" jsonschema:"board (counts and goals, the default), goals, asks, decisions, questions, work or ready"`
	Limit  int    `json:"limit,omitempty" jsonschema:"entries per page, 1 to 100; default 25"`
	Cursor string `json:"cursor,omitempty" jsonschema:"nextCursor from a previous call"`
}

func (d *Daemon) mcpLedgerRead(_ context.Context, caller *mcpCaller, input mcpLedgerInput) (any, error) {
	if !caller.has("ledger.read") {
		return nil, refuse("capability_missing", "Reading the ledger needs ledger.read.")
	}
	if caller.project == nil || caller.project.Key == "" {
		return nil, refuse("invalid", "Your session is not in a project with a ledger; the person adds one with agenthail project add.")
	}
	board, err := d.ledgerStore().Board(caller.project.Key)
	if err != nil {
		return nil, err
	}
	lists := map[string]func() any{
		"goals":     func() any { return board.Goals() },
		"asks":      func() any { return board.AllAsks() },
		"decisions": func() any { return board.Decisions() },
		"questions": func() any { return board.Questions() },
		"work":      func() any { return board.WorkItems() },
		"ready":     func() any { return board.Ready() },
	}
	view := input.View
	if view == "" || view == "board" {
		counts := map[string]int{}
		for name, list := range lists {
			counts[name] = len(rawEntries(list()))
		}
		goals, nextCursor, truncated := pageEntries(board.Goals(), 0, 25)
		result := map[string]any{"projectKey": caller.project.Key, "counts": counts, "goals": goals, "truncated": truncated}
		if truncated {
			result["nextCursor"] = nextCursor
			result["view"] = "goals"
		}
		return result, nil
	}
	list, known := lists[view]
	if !known {
		return nil, refuse("invalid", fmt.Sprintf("Unknown view %q; use board, goals, asks, decisions, questions, work or ready.", view))
	}
	limit, err := pageLimit(input.Limit, 25, 100)
	if err != nil {
		return nil, err
	}
	offset, err := decodeOffsetCursor(input.Cursor)
	if err != nil {
		return nil, err
	}
	entries, nextCursor, truncated := pageEntries(list(), offset, limit)
	result := map[string]any{"projectKey": caller.project.Key, "view": view, view: entries, "truncated": truncated}
	if truncated {
		result["nextCursor"] = nextCursor
	}
	return result, nil
}

func rawEntries(list any) []json.RawMessage {
	encoded, _ := json.Marshal(list)
	var all []json.RawMessage
	_ = json.Unmarshal(encoded, &all)
	return all
}

func pageEntries(list any, offset, limit int) ([]json.RawMessage, string, bool) {
	all := rawEntries(list)
	entries := []json.RawMessage{}
	used, next := 0, offset
	for ; next < len(all) && len(entries) < limit; next++ {
		if used+len(all[next]) > mcpPageBudget && len(entries) > 0 {
			break
		}
		used += len(all[next])
		entries = append(entries, all[next])
	}
	return entries, strconv.Itoa(next), next < len(all)
}
