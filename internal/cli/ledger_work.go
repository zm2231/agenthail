package cli

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/zm2231/agenthail/internal/ledger"
)

const ledgerWorkUsage = `usage: agenthail ledger work [--mine] [--found] [--state <state>] [--type <type>] [--priority <P0-P3>] [--label <label>] [--all]
       agenthail ledger work <item_id>
       agenthail ledger work <critical-path|ready|next>
       agenthail ledger work add "<text>" [--clause <clause_id> | --found-in <item|clause>] [--type task|bug|feature] [--priority P0-P3]
                                          [--description "..."] [--acceptance "..."] [--label <label>]...
       (work create and work new are short for work add)
       agenthail ledger work edit <item_id> [--text "..."] [--type ...] [--priority ...] [--description "..."] [--acceptance "..."] [--label ...] [--unlabel ...]   (or work update)
       agenthail ledger work link <item_id> <clause_id>
       agenthail ledger work comment <item_id> "<text>"
       agenthail ledger work log <item_id> "<text>" [--type progress|blocker|decision|hypothesis|tried|result]
       agenthail ledger work <claim|block|unblock|submit|reopen|restore> <item_id> [--reason "..."]
       agenthail ledger work close <item_id> --reason "duplicate|wontfix|obsolete ..."
       agenthail ledger work delete <item_id> [--reason "..."]
       agenthail ledger work <approve|reject> <item_id> [--utterance <the person's words>] [--reason "..."]
       agenthail ledger work dep <item_id> --on <item_id>
       agenthail ledger work handoff <item_id> [--done "..."] [--remaining "..."] [--decisions "..."] [--uncertain "..."]`

var workActionAliases = map[string]ledger.WorkAction{"create": ledger.WorkAdd, "new": ledger.WorkAdd, "update": ledger.WorkEdit,
	"start": ledger.WorkClaim, "review": ledger.WorkSubmit}

func (a *App) ledgerWork(store *ledger.Store, args []string) error {
	w, err := parseLedgerWrite(args)
	if err != nil {
		return err
	}
	if len(w.positional) >= 2 && w.positional[1] == "list" {
		w.positional = slices.Delete(w.positional, 1, 2)
	}
	if len(w.positional) == 3 && w.positional[1] == "show" {
		return a.ledgerWorkShow(store, w, w.positional[2])
	}
	if len(w.positional) < 2 {
		return a.ledgerWorkList(store, w)
	}
	switch view := w.positional[1]; view {
	case "critical-path", "ready", "next":
		if len(w.positional) != 2 {
			return fmt.Errorf("%s takes no arguments\n%s", view, ledgerWorkUsage)
		}
		if view == "critical-path" {
			return a.ledgerCriticalPath(store, w)
		}
		return a.ledgerWorkReady(store, w, view == "next")
	}
	action := ledger.WorkAction(w.positional[1])
	if alias, ok := workActionAliases[w.positional[1]]; ok {
		action = alias
	}
	if len(w.positional) == 2 && !workActions[action] {
		return a.ledgerWorkShow(store, w, w.positional[1])
	}
	req := ledger.WorkRequest{Action: action, Reason: w.value("--reason"), DependsOn: w.value("--on"), UtteranceID: w.value("--utterance"), LogType: w.value("--type"),
		Handoff: ledger.Handoff{Done: w.values["--done"], Remaining: w.values["--remaining"], Decisions: w.values["--decisions"], Uncertain: w.values["--uncertain"]}}
	note := action == ledger.WorkComment || action == ledger.WorkLog
	switch {
	case action == ledger.WorkAdd && len(w.positional) >= 3:
		if len(w.values["--clause"]) > 1 {
			return fmt.Errorf("a work item hangs off one clause at most\n%s", ledgerWorkUsage)
		}
		req.ClauseID, req.Text, req.LogType = w.value("--clause"), strings.Join(w.positional[2:], " "), ""
		req.Fields = ledger.WorkFields{Type: ledger.WorkType(w.value("--type")), Priority: w.value("--priority"), Description: w.value("--description"),
			Acceptance: w.value("--acceptance"), Labels: w.values["--label"], From: w.value("--found-in")}
	case action == ledger.WorkEdit && len(w.positional) == 3:
		req.ItemID, req.LogType = w.positional[2], ""
		req.Change = workChange(w)
	case action == ledger.WorkLink && len(w.positional) == 4:
		req.ItemID, req.ClauseID = w.positional[2], w.positional[3]
	case note && len(w.positional) >= 4:
		req.ItemID, req.Text = w.positional[2], strings.Join(w.positional[3:], " ")
	case action != ledger.WorkAdd && action != ledger.WorkEdit && action != ledger.WorkLink && !note && len(w.positional) == 3:
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

func workChange(w ledgerWriteArgs) ledger.WorkChange {
	set := func(flag string) *string {
		if v, ok := w.values[flag]; ok && len(v) > 0 {
			return &v[0]
		}
		return nil
	}
	change := ledger.WorkChange{Text: set("--text"), Priority: set("--priority"), Description: set("--description"), Acceptance: set("--acceptance"),
		AddLabels: w.values["--label"], RemoveLabels: w.values["--unlabel"]}
	if t := set("--type"); t != nil {
		typ := ledger.WorkType(*t)
		change.Type = &typ
	}
	return change
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
	filter := workFilter{mine: mine, state: ledger.WorkState(w.value("--state")), typ: ledger.WorkType(strings.ToLower(w.value("--type"))),
		priority: strings.ToUpper(w.value("--priority")), label: strings.ToLower(w.value("--label")), found: w.bools["--found"], all: w.bools["--all"]}
	var items []ledger.WorkItem
	for _, item := range board.WorkItems() {
		if filter.keeps(item) {
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
	ledger.WorkComment: true, ledger.WorkLog: true, ledger.WorkEdit: true, ledger.WorkLink: true, ledger.WorkClose: true,
	ledger.WorkDelete: true, ledger.WorkRestore: true,
}

// workFilter keeps unsettled items by default; --all adds done and closed ones, and deleted ones show only when asked for by state.
type workFilter struct {
	mine, priority, label string
	state                 ledger.WorkState
	typ                   ledger.WorkType
	found, all            bool
}

func (f workFilter) keeps(item ledger.WorkItem) bool {
	switch {
	case f.mine != "" && item.Assignee != f.mine,
		f.typ != "" && item.Type != f.typ,
		f.priority != "" && item.Priority != f.priority && "P"+f.priority != item.Priority,
		f.label != "" && !slices.Contains(item.Labels, f.label),
		f.found && item.ClauseID != "":
		return false
	case f.state != "":
		return item.State == f.state
	case item.State == ledger.WorkDeleted:
		return false
	case f.all:
		return true
	}
	return item.State != ledger.WorkDone && item.State != ledger.WorkClosed
}

func workLine(item ledger.WorkItem) string {
	on := "on " + item.ClauseID
	if item.ClauseID == "" {
		on = "found"
		if item.From != "" {
			on += " in " + item.From
		}
	}
	line := fmt.Sprintf("%s [%s] %s %s %s: %s", item.ID, item.State, item.Priority, item.Type, on, item.Text)
	if len(item.Labels) > 0 {
		line += " #" + strings.Join(item.Labels, " #")
	}
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
