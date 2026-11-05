package cli

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
)

type ledgerCall struct {
	store      *ledger.Store
	args       []string
	positional []string
}

var ledgerCommands = map[string]func(a *App, c ledgerCall) error{
	"asks": func(a *App, c ledgerCall) error { return a.ledgerAsks(c.store, c.args) },
	"ask": func(a *App, c ledgerCall) error {
		if len(c.positional) < 2 {
			return fmt.Errorf("usage: agenthail ledger ask <ask_id>")
		}
		return a.ledgerAsk(c.store, c.positional[1], c.args)
	},
	"show": func(a *App, c ledgerCall) error {
		if len(c.positional) != 2 {
			return fmt.Errorf("usage: agenthail ledger show <item_id|ask_id>")
		}
		return a.ledgerShow(c.store, c.args, c.positional[1])
	},
	"enable":       func(a *App, c ledgerCall) error { return a.ledgerEnable(c.store, c.args) },
	"disable":      func(a *App, c ledgerCall) error { return a.ledgerDisable(c.store, c.args) },
	"goal":         func(a *App, c ledgerCall) error { return a.ledgerGoal(c.args) },
	"goals":        func(a *App, c ledgerCall) error { return a.ledgerGoals(c.store, c.args) },
	"attach":       func(a *App, c ledgerCall) error { return a.ledgerAttach(c.args, false) },
	"propose":      func(a *App, c ledgerCall) error { return a.ledgerAttach(c.args, true) },
	"question":     func(a *App, c ledgerCall) error { return a.ledgerQuestion(c.args) },
	"questions":    func(a *App, c ledgerCall) error { return a.ledgerQuestions(c.store, c.args) },
	"work":         func(a *App, c ledgerCall) error { return a.ledgerWork(c.store, c.args) },
	"import":       func(a *App, c ledgerCall) error { return a.ledgerImport(c.store, c.args) },
	"owner":        func(a *App, c ledgerCall) error { return a.ledgerOwner(c.store, c.args) },
	"digest":       func(a *App, c ledgerCall) error { return a.ledgerDigest(c.store, c.args) },
	"own":          func(a *App, c ledgerCall) error { return a.ledgerClause(ledger.ClauseOwn, c.args) },
	"progress":     func(a *App, c ledgerCall) error { return a.ledgerClause(ledger.ClauseProgress, c.args) },
	"wait":         func(a *App, c ledgerCall) error { return a.ledgerClause(ledger.ClauseWait, c.args) },
	"check":        func(a *App, c ledgerCall) error { return a.ledgerCheck(c.args) },
	"check-report": func(a *App, c ledgerCall) error { return a.ledgerCheckReport(c.args) },
}

func (a *App) cmdLedger(args []string) error {
	at, help := ledgerPositionals(args)
	positional := make([]string, len(at))
	for i, j := range at {
		positional[i] = args[j]
	}
	if help || len(positional) > 0 && (positional[0] == "help" || positional[0] == "-h") {
		fmt.Print(ledgerHelp)
		return nil
	}
	r := a.registry()
	store := ledger.NewStore(r.WriteDB(), r.ReadDB())
	if len(positional) == 0 {
		return a.ledgerBoard(store, args)
	}
	if ledgerWorkShortForms[positional[0]] {
		return a.ledgerWork(store, workArgs(args, at[0]))
	}
	run, ok := ledgerCommands[positional[0]]
	if !ok {
		return fmt.Errorf("unknown ledger command %q\n\n%s", positional[0], ledgerHelp)
	}
	return run(a, ledgerCall{store: store, args: args, positional: positional})
}

func ledgerProjectKey(a *App) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	project, err := daemon.ResolvePathProject(context.Background(), a.registry(), cwd)
	if err != nil {
		return "", err
	}
	if project == nil || project.Key == "" {
		return "", fmt.Errorf("no project found for %s; run 'agenthail project add' first", cwd)
	}
	return project.Key, nil
}

func (a *App) ledgerBoard(store *ledger.Store, args []string) error {
	key, err := ledgerProjectKey(a)
	if err != nil {
		return err
	}
	scope, err := store.Scope(key)
	if err != nil {
		return err
	}
	if scope.State == ledger.LedgerDisabled {
		if hasFlag(args, "--json") {
			return emit(map[string]any{"projectKey": key, "capture": "disabled"})
		}
		fmt.Printf("ledger is disabled for this project (%s)\n", key)
		fmt.Println("enable with: agenthail ledger enable")
		return nil
	}
	capture, coverage := "lead", "capturing the person's words to the project's lead; nothing asks it to attach them. To ask every session to attach each message: agenthail ledger enable"
	if scope.State == ledger.LedgerEnabled {
		capture = "enabled"
		coverage = "capturing since " + scope.Since.Local().Format(time.DateTime) + "; words from before then are not in the ledger"
	}

	board, err := store.Board(key)
	if err != nil {
		return err
	}
	attachment, err := store.Attachment(key, board)
	if err != nil {
		return err
	}
	view := newBoardView(board)
	g2, g2Err := a.ledgerG2(key)
	hookProblems, hookErr := ledgerPromptHookProblems(context.Background(), a.registry())

	if hasFlag(args, "--json") {
		out := map[string]any{
			"projectKey":         key,
			"capture":            capture,
			"goals":              view.goals,
			"asks":               view.asks,
			"decisions":          view.decisions,
			"projectConstraints": view.constraints,
			"questions":          view.questions,
			"targetCuts":         view.targetCuts,
			"g2Waivers":          view.waivers,
			"retractions":        append([]ledger.Retraction{}, view.retractions...),
			"checks":             board.GoalChecks(),
			"unattached":         attachment.Unattached,
			"attachedElsewhere":  attachment.Elsewhere,
		}
		if scope.State == ledger.LedgerEnabled {
			out["captureSince"] = scope.Since
		}
		if g2Err != nil {
			out["g2Error"] = g2Err.Error()
		} else {
			out["g2"] = g2
		}
		out["promptHookProblems"] = append([]promptHookProblem{}, hookProblems...)
		if hookErr != nil {
			out["promptHooksError"] = hookErr.Error()
		}
		return emit(out)
	}

	fmt.Println(coverage)
	for _, problem := range hookProblems {
		fmt.Println(problem)
	}
	if hookErr != nil {
		fmt.Printf("prompt hooks: unknown (%s)\n", hookErr)
	}
	if scope.State == ledger.LedgerEnabled {
		fmt.Println(g2Line(g2, g2Err))
		view.print(attachment, 0)
		return nil
	}
	shown := ledgerLeadWordsShown
	if hasFlag(args, "--all") {
		shown = 0
	}
	view.print(attachment, shown)
	return nil
}

const ledgerLeadWordsShown = 5

func (a *App) ledgerAsks(store *ledger.Store, args []string) error {
	key, err := ledgerProjectKey(a)
	if err != nil {
		return err
	}
	board, err := store.Board(key)
	if err != nil {
		return err
	}
	asks := board.AllAsks()
	if hasFlag(args, "--json") {
		return emit(asks)
	}
	if len(asks) == 0 {
		fmt.Println("no asks")
		return nil
	}
	for _, ask := range asks {
		fmt.Printf("%s [%s] %d clauses\n", ask.ID, ask.Type, len(ask.Clauses))
	}
	return nil
}

func (a *App) ledgerAsk(store *ledger.Store, askID string, args []string) error {
	key, err := ledgerProjectKey(a)
	if err != nil {
		return err
	}
	board, err := store.Board(key)
	if err != nil {
		return err
	}
	for _, ask := range board.AllAsks() {
		if ask.ID == askID {
			if hasFlag(args, "--json") {
				return emit(ask)
			}
			fmt.Printf("%s [%s]\n", ask.ID, ask.Type)
			for _, c := range ask.Clauses {
				fmt.Printf("  %s: %s [%s]\n", c.ID, c.Text, c.State)
			}
			return nil
		}
	}
	return fmt.Errorf("ask %s not found", askID)
}

func (a *App) ledgerSessionArg(args []string) (*surface.Session, error) {
	positional := stripFlags(args)
	if len(positional) < 2 {
		return nil, nil
	}
	session, _, err := a.resolveTarget(context.Background(), positional[1])
	return session, err
}

func (a *App) ledgerEnable(store *ledger.Store, args []string) error {
	key, err := ledgerProjectKey(a)
	if err != nil {
		return err
	}
	session, err := a.ledgerSessionArg(args)
	if err != nil {
		return err
	}
	if session != nil {
		if err := store.GrantSession(key, session.ID); err != nil {
			return err
		}
		if hasFlag(args, "--json") {
			return emit(map[string]any{"projectKey": key, "sessionId": session.ID, "granted": true})
		}
		fmt.Printf("ledger captures %s for %s\n", session.ID, key)
		return nil
	}
	if err := store.Enable(key); err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return emit(map[string]any{"projectKey": key, "enabled": true})
	}
	fmt.Printf("ledger enabled for %s\n", key)
	return nil
}

func (a *App) ledgerDisable(store *ledger.Store, args []string) error {
	key, err := ledgerProjectKey(a)
	if err != nil {
		return err
	}
	session, err := a.ledgerSessionArg(args)
	if err != nil {
		return err
	}
	if session != nil {
		if err := store.RevokeSession(key, session.ID); err != nil {
			return err
		}
		if hasFlag(args, "--json") {
			return emit(map[string]any{"projectKey": key, "sessionId": session.ID, "granted": false})
		}
		fmt.Printf("ledger no longer captures %s for %s\n", session.ID, key)
		return nil
	}
	if err := store.Disable(key); err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return emit(map[string]any{"projectKey": key, "enabled": false})
	}
	fmt.Printf("ledger disabled for %s\n", key)
	return nil
}

var ledgerWorkShortForms = map[string]bool{"add": true, "create": true, "new": true, "list": true, "edit": true, "update": true,
	"start": true, "claim": true, "log": true, "comment": true, "handoff": true, "block": true, "unblock": true, "review": true, "submit": true,
	"approve": true, "reject": true, "close": true, "reopen": true, "delete": true, "restore": true, "dep": true, "link": true,
	"ready": true, "next": true, "critical-path": true, "search": true}

func workArgs(args []string, i int) []string {
	if args[i] == "list" {
		return slices.Concat(args[:i], []string{"work"}, args[i+1:])
	}
	return slices.Concat(args[:i], []string{"work"}, args[i:])
}

func ledgerPositionals(args []string) (at []int, help bool) {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--":
			for j := i + 1; j < len(args); j++ {
				at = append(at, j)
			}
			return at, help
		case ledgerWriteValueFlags[a]:
			i++
		case a == "--help":
			help = true
		case strings.HasPrefix(a, "--"):
		default:
			at = append(at, i)
		}
	}
	return at, help
}

func (a *App) ledgerShow(store *ledger.Store, args []string, id string) error {
	w, err := parseLedgerWrite(args)
	if err != nil {
		return err
	}
	board, err := a.workBoard(store, w)
	if err != nil {
		return err
	}
	if _, ok := board.WorkItem(id); ok {
		return a.ledgerWorkShow(store, w, id)
	}
	if item, ok := board.WorkItemByOrigin(id); ok {
		return a.ledgerWorkShow(store, w, item.ID)
	}
	for _, ask := range board.AllAsks() {
		if ask.ID == id {
			return a.ledgerAsk(store, id, args)
		}
	}
	return fmt.Errorf("%q is not a work item or an ask in this project; agenthail ledger list shows the items", id)
}
