package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zm2231/agenthail/internal/ledger"
)

const ledgerWorkUsage = `usage: agenthail ledger work [--mine]
       agenthail ledger work critical-path
       agenthail ledger work add <clause_id> "<text>"
       agenthail ledger work <claim|block|unblock|submit|reopen> <item_id> [--reason "..."]
       agenthail ledger work <approve|reject> <item_id> [--utterance <the person's words>] [--reason "..."]
       agenthail ledger work dep <item_id> --on <item_id>
       agenthail ledger work handoff <item_id> [--done "..."] [--remaining "..."] [--decisions "..."] [--uncertain "..."]`

func (a *App) ledgerWork(store *ledger.Store, args []string) error {
	w, err := parseLedgerWrite(args)
	if err != nil {
		return err
	}
	if len(w.positional) < 2 {
		return a.ledgerWorkList(store, w)
	}
	if w.positional[1] == "critical-path" {
		return a.ledgerCriticalPath(store, w)
	}
	action := ledger.WorkAction(w.positional[1])
	req := ledger.WorkRequest{Action: action, Reason: w.value("--reason"), DependsOn: w.value("--on"), UtteranceID: w.value("--utterance"),
		Handoff: ledger.Handoff{Done: w.values["--done"], Remaining: w.values["--remaining"], Decisions: w.values["--decisions"], Uncertain: w.values["--uncertain"]}}
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

func (a *App) workBoard(store *ledger.Store, w ledgerWriteArgs) (*ledger.Board, error) {
	key := w.value("--project")
	if key == "" {
		var err error
		if key, err = ledgerProjectKey(a); err != nil {
			return nil, err
		}
	}
	return store.Board(key)
}

func (a *App) ledgerWorkList(store *ledger.Store, w ledgerWriteArgs) error {
	board, err := a.workBoard(store, w)
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

func (a *App) ledgerCriticalPath(store *ledger.Store, w ledgerWriteArgs) error {
	board, err := a.workBoard(store, w)
	if err != nil {
		return err
	}
	path := board.CriticalPath()
	if w.bools["--json"] {
		return emit(path)
	}
	if len(path) == 0 {
		fmt.Println("no unfinished work items")
		return nil
	}
	for i, item := range path {
		fmt.Printf("%d. %s\n", i+1, workLine(item))
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
	if len(item.DependsOn) > 0 {
		line += " waits on " + strings.Join(item.DependsOn, ", ")
	}
	if item.Reviewer != "" {
		line += " reviewed by " + item.Reviewer
	}
	if h := item.Handoff; h != nil {
		line += fmt.Sprintf(" handoff from %s: done %q, remaining %q, decisions %q, uncertain %q", h.By, h.Done, h.Remaining, h.Decisions, h.Uncertain)
	}
	return line
}
