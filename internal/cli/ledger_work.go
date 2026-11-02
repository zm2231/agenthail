package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zm2231/agenthail/internal/ledger"
)

const ledgerWorkUsage = `usage: agenthail ledger work [--mine]
       agenthail ledger work <item_id>
       agenthail ledger work <critical-path|ready|next>
       agenthail ledger work add <clause_id> "<text>"
       agenthail ledger work comment <item_id> "<text>"
       agenthail ledger work log <item_id> "<text>" [--type progress|blocker|decision|hypothesis|tried|result]
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
	switch w.positional[1] {
	case "critical-path":
		return a.ledgerCriticalPath(store, w)
	case "ready", "next":
		return a.ledgerWorkReady(store, w, w.positional[1] == "next")
	}
	action := ledger.WorkAction(w.positional[1])
	if len(w.positional) == 2 && !workActions[action] {
		return a.ledgerWorkShow(store, w, w.positional[1])
	}
	req := ledger.WorkRequest{Action: action, Reason: w.value("--reason"), DependsOn: w.value("--on"), UtteranceID: w.value("--utterance"), LogType: w.value("--type"),
		Handoff: ledger.Handoff{Done: w.values["--done"], Remaining: w.values["--remaining"], Decisions: w.values["--decisions"], Uncertain: w.values["--uncertain"]}}
	note := action == ledger.WorkComment || action == ledger.WorkLog
	switch {
	case action == ledger.WorkAdd && len(w.positional) >= 4:
		req.ClauseID, req.Text = w.positional[2], strings.Join(w.positional[3:], " ")
	case note && len(w.positional) >= 4:
		req.ItemID, req.Text = w.positional[2], strings.Join(w.positional[3:], " ")
	case action != ledger.WorkAdd && !note && len(w.positional) == 3:
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

var workActions = map[ledger.WorkAction]bool{
	ledger.WorkAdd: true, ledger.WorkClaim: true, ledger.WorkBlock: true, ledger.WorkUnblock: true, ledger.WorkSubmit: true,
	ledger.WorkApprove: true, ledger.WorkReject: true, ledger.WorkReopen: true, ledger.WorkHandoff: true, ledger.WorkDep: true,
	ledger.WorkComment: true, ledger.WorkLog: true,
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
	if comments, logs := workNoteCounts(item); comments+logs > 0 {
		line += fmt.Sprintf(" (%d comments, %d log entries)", comments, logs)
	}
	return line
}

func workNoteCounts(item ledger.WorkItem) (comments, logs int) {
	for _, e := range item.Timeline {
		switch e.Kind {
		case "comment":
			comments++
		case "log":
			logs++
		}
	}
	return comments, logs
}
