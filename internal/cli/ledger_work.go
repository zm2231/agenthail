package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zm2231/agenthail/internal/ledger"
)

const ledgerWorkUsage = `usage: agenthail ledger work [--mine]
       agenthail ledger work add <clause_id> "<text>"
       agenthail ledger work <claim|block|unblock|done|reopen> <item_id> [--reason "..."]`

func (a *App) ledgerWork(store *ledger.Store, args []string) error {
	w, err := parseLedgerWrite(args)
	if err != nil {
		return err
	}
	if len(w.positional) < 2 {
		return a.ledgerWorkList(store, w)
	}
	action := ledger.WorkAction(w.positional[1])
	req := ledger.WorkRequest{Action: action, Reason: w.value("--reason")}
	switch {
	case action == ledger.WorkAdd && len(w.positional) >= 4:
		req.ClauseID, req.Text = w.positional[2], strings.Join(w.positional[3:], " ")
	case action != ledger.WorkAdd && len(w.positional) == 3:
		req.ItemID = w.positional[2]
	default:
		return fmt.Errorf("%s", ledgerWorkUsage)
	}
	ctx := context.Background()
	if req.ProjectKey, req.SessionID, err = a.ledgerWriteContext(ctx, w); err != nil {
		return err
	}
	var result ledger.WorkResult
	if err := a.ledgerPost(ctx, "/api/v1/ledger/work", req, &result); err != nil {
		return err
	}
	if w.bools["--json"] {
		return emit(result)
	}
	fmt.Println(result.Readback)
	return nil
}

func (a *App) ledgerWorkList(store *ledger.Store, w ledgerWriteArgs) error {
	key := w.value("--project")
	if key == "" {
		var err error
		if key, err = ledgerProjectKey(a); err != nil {
			return err
		}
	}
	board, err := store.Board(key)
	if err != nil {
		return err
	}
	mine := ""
	if w.bools["--mine"] {
		if mine, err = a.sourceSessionID(context.Background(), w.value("--from")); err != nil {
			return err
		}
		if mine == "" {
			return fmt.Errorf("--mine needs to run inside a session, or pass --from <session>")
		}
	}
	var items []ledger.WorkItem
	for _, item := range board.WorkItems() {
		if mine == "" || item.Assignee == mine {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return ledgerIDNumber(items[i].ID) < ledgerIDNumber(items[j].ID) })
	if w.bools["--json"] {
		return emit(items)
	}
	if len(items) == 0 {
		fmt.Println("no work items")
		return nil
	}
	for _, item := range items {
		fmt.Println(workLine(item))
	}
	return nil
}

func workLine(item ledger.WorkItem) string {
	line := fmt.Sprintf("%s [%s] on %s: %s", item.ID, item.State, item.ClauseID, item.Text)
	if item.Assignee != "" {
		line += " assignee=" + item.Assignee
	}
	if item.State == ledger.WorkBlocked && item.Reason != "" {
		line += " blocked on: " + item.Reason
	}
	return line
}
