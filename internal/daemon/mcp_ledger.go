package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

type mcpLedgerInput struct {
	View   string `json:"view,omitempty" jsonschema:"board (counts and goals, the default), goals, asks, decisions, questions, work, or ready (the next work, ranked)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"entries per page, 1 to 100 (ready: 1 to 50); default 25"`
	Cursor string `json:"cursor,omitempty" jsonschema:"nextCursor from a previous call"`
}

type ledgerEntry struct {
	id  string
	raw json.RawMessage
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
	}
	key := caller.project.Key
	switch view := input.View; view {
	case "", "board":
		counts := map[string]int{"ready": len(board.Ready())}
		for name, list := range lists {
			counts[name] = len(ledgerEntries(list()))
		}
		goals, nextCursor := pageLedger(ledgerEntries(board.Goals()), "", 25)
		result := map[string]any{"projectKey": key, "counts": counts, "goals": goals, "truncated": nextCursor != ""}
		if nextCursor != "" {
			result["nextCursor"], result["view"] = nextCursor, "goals"
		}
		return result, nil
	case "ready":
		limit, err := pageLimit(input.Limit, 25, 50)
		if err != nil {
			return nil, err
		}
		ready := board.Ready()
		rows := []map[string]any{}
		for rank, item := range ready[:min(limit, len(ready))] {
			rows = append(rows, map[string]any{"rank": rank + 1, "id": item.ID, "text": clipUTF8(item.Text, 240), "state": item.State, "assignee": item.Assignee})
		}
		return map[string]any{"projectKey": key, "view": view, "ready": rows, "total": len(ready), "truncated": len(rows) < len(ready)}, nil
	default:
		list, known := lists[view]
		if !known {
			return nil, refuse("invalid", fmt.Sprintf("Unknown view %q; use board, goals, asks, decisions, questions, work or ready.", view))
		}
		limit, err := pageLimit(input.Limit, 25, 100)
		if err != nil {
			return nil, err
		}
		entries, nextCursor := pageLedger(ledgerEntries(list()), input.Cursor, limit)
		result := map[string]any{"projectKey": key, "view": view, view: entries, "truncated": nextCursor != ""}
		if nextCursor != "" {
			result["nextCursor"] = nextCursor
		}
		return result, nil
	}
}

func ledgerEntries(list any) []ledgerEntry {
	encoded, _ := json.Marshal(list)
	var raw []json.RawMessage
	_ = json.Unmarshal(encoded, &raw)
	entries := make([]ledgerEntry, 0, len(raw))
	for _, item := range raw {
		var identity struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(item, &identity)
		entries = append(entries, ledgerEntry{id: identity.ID, raw: item})
	}
	slices.SortFunc(entries, func(a, b ledgerEntry) int { return compareLedgerIDs(a.id, b.id) })
	return entries
}

// Ledger ids end in a per-kind counter, so ordering by it lists entries in the order they were made.
func compareLedgerIDs(a, b string) int {
	headA, numberA := splitLedgerID(a)
	headB, numberB := splitLedgerID(b)
	return cmp.Or(strings.Compare(headA, headB), cmp.Compare(numberA, numberB), strings.Compare(a, b))
}

func splitLedgerID(id string) (string, int) {
	digits := len(id)
	for digits > 0 && id[digits-1] >= '0' && id[digits-1] <= '9' {
		digits--
	}
	number, _ := strconv.Atoi(id[digits:])
	return id[:digits], number
}

func pageLedger(entries []ledgerEntry, cursor string, limit int) ([]json.RawMessage, string) {
	start := 0
	if cursor != "" {
		start = len(entries)
		for index, entry := range entries {
			if compareLedgerIDs(entry.id, cursor) > 0 {
				start = index
				break
			}
		}
	}
	page := []json.RawMessage{}
	used, next := 0, start
	for ; next < len(entries) && len(page) < limit; next++ {
		if used+len(entries[next].raw) > mcpPageBudget && len(page) > 0 {
			break
		}
		used += len(entries[next].raw)
		page = append(page, entries[next].raw)
	}
	if next < len(entries) {
		return page, entries[next-1].id
	}
	return page, ""
}
