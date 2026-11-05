package cli

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/zm2231/agenthail/internal/ledger"
)

const ledgerWorkUsage = `usage: agenthail ledger work [list] [--state <s,...>] [--type <t,...>] [--priority <P0,...>] [--label <l>]... [--assignee <session>|--mine]
                             [--clause <id>] [--found] [--sort id|priority|created|updated] [--limit <n>] [--all]
       agenthail ledger work search <words>... [the same filters]
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
       agenthail ledger work close <item_id> --reason "done: <what finished it>|duplicate|wontfix|obsolete ..."
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
		return a.ledgerWorkList(store, w, nil)
	}
	if w.positional[1] == "search" {
		if len(w.positional) < 3 {
			return fmt.Errorf("usage: agenthail ledger search <words>... [filters]\n%s", ledgerWorkUsage)
		}
		return a.ledgerWorkList(store, w, w.positional[2:])
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
	var checkpoint string
	if req.Action.AttachCheckpoint() {
		checkpoint = a.ledgerCheckpoint(ctx, req.SessionID)
	}
	if w.bools["--json"] {
		return emit(struct {
			ledger.WorkResult
			LedgerCheckpoint string `json:"ledgerCheckpoint,omitempty"`
		}{result, checkpoint})
	}
	fmt.Println(result.Readback)
	if checkpoint != "" {
		fmt.Println("\n" + checkpoint)
	}
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

func (a *App) ledgerWorkList(store *ledger.Store, w ledgerWriteArgs, terms []string) error {
	board, err := a.workBoard(store, w)
	if err != nil {
		return err
	}
	q, err := a.workQuery(w, terms)
	if err != nil {
		return err
	}
	items, err := board.QueryWork(q)
	if err != nil {
		return err
	}
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

func (a *App) workQuery(w ledgerWriteArgs, terms []string) (ledger.WorkQuery, error) {
	q := ledger.WorkQuery{Terms: terms, All: w.bools["--all"] || len(terms) > 0, Labels: w.values["--label"], Assignee: w.value("--assignee"),
		Clause: w.value("--clause"), Found: w.bools["--found"], Sort: w.value("--sort")}
	for _, s := range commaList(w.value("--state")) {
		q.States = append(q.States, ledger.WorkState(s))
	}
	for _, t := range commaList(w.value("--type")) {
		q.Types = append(q.Types, ledger.WorkType(t))
	}
	q.Priorities = commaList(w.value("--priority"))
	if limit := w.value("--limit"); limit != "" {
		n, err := strconv.Atoi(limit)
		if err != nil {
			return q, fmt.Errorf("--limit is a number of items, got %q", limit)
		}
		q.Limit = n
	}
	if w.bools["--mine"] {
		mine, err := a.sourceSessionID(context.Background(), w.value("--from"))
		if err != nil {
			return q, err
		}
		if mine == "" {
			return q, fmt.Errorf("--mine needs to run inside a session, or pass --from <session>")
		}
		q.Assignee = mine
	}
	return q, nil
}

func commaList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
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
