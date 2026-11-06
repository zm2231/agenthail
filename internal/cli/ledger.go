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
	"github.com/zm2231/agenthail/internal/registry"
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
	"enable":       func(a *App, c ledgerCall) error { return a.ledgerEnable(c.args) },
	"disable":      func(a *App, c ledgerCall) error { return a.ledgerDisable(c.args) },
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
	return daemon.ResolvePathLedgerKey(context.Background(), a.registry(), cwd)
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
	notCarried, err := a.registry().ProjectMoveRefusalsOf(key)
	if err != nil {
		return err
	}
	if !hasFlag(args, "--json") {
		for _, refusal := range notCarried {
			fmt.Println(notCarriedLine(refusal))
		}
	}
	if scope.State == ledger.LedgerDisabled {
		if hasFlag(args, "--json") {
			return emit(map[string]any{"projectKey": key, "capture": "disabled", "notCarried": notCarried})
		}
		fmt.Printf("ledger is disabled for this project (%s)\n", key)
		fmt.Println("enable with: agenthail ledger enable")
		return nil
	}
	capture, coverage := "lead", "capturing the person's words to the project's lead; nothing asks it to attach them. To ask every session to attach each message: agenthail ledger enable"
	if scope.State == ledger.LedgerEnabled {
		capture = "enabled"
		coverage = "capturing since " + scope.Since.Local().Format(time.DateTime) + "; words from before then are not in the ledger"
		if scope.Attach == ledger.AttachAtCheckpoints {
			coverage += "\nsessions attach the person's words at checkpoints: adding, claiming or submitting work, starting or compacting, and here"
		}
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
			"notCarried":         notCarried,
		}
		if scope.State == ledger.LedgerEnabled {
			out["captureSince"], out["attach"] = scope.Since, scope.Attach
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
		if scope.Attach == ledger.AttachAtCheckpoints && len(attachment.Unattached) > 0 {
			fmt.Println(ledgerAttachInOneCall)
		}
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

const ledgerAttachInOneCall = `attach your own in one call, one fragment per intent:
  agenthail ledger attach <utterance> --as <...> --to <...> --clause "..." --and <utterance> --as ...`

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

func (a *App) ledgerEnable(args []string) error {
	w, err := parseLedgerWrite(args)
	if err != nil {
		return err
	}
	mode, err := ledger.ParseAttachMode(w.value("--attach"))
	if err != nil {
		return err
	}
	if len(w.positional) > 1 && w.value("--attach") != "" {
		return fmt.Errorf("--attach sets when the whole project attaches; a session you add follows it")
	}
	answer, err := a.ledgerSetScope(w.positional, true, mode)
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return emit(answer)
	}
	if answer.SessionID != "" {
		fmt.Printf("ledger captures %s for %s\n", answer.SessionID, answer.ProjectKey)
		return nil
	}
	if answer.Attach == ledger.AttachAtCheckpoints {
		fmt.Printf("ledger enabled for %s; sessions attach the person's words at checkpoints: adding, claiming or submitting work, starting or compacting, and agenthail ledger\n", answer.ProjectKey)
		return nil
	}
	fmt.Printf("ledger enabled for %s; sessions attach each of the person's messages before replying\n", answer.ProjectKey)
	return nil
}

func (a *App) ledgerDisable(args []string) error {
	answer, err := a.ledgerSetScope(stripFlags(args), false, "")
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return emit(answer)
	}
	if answer.SessionID != "" {
		fmt.Printf("ledger no longer captures %s for %s\n", answer.SessionID, answer.ProjectKey)
		return nil
	}
	fmt.Printf("ledger disabled for %s\n", answer.ProjectKey)
	return nil
}

type ledgerScopeAnswer struct {
	Attach     ledger.AttachMode `json:"attach,omitempty"`
	Enabled    *bool             `json:"enabled,omitempty"`
	Granted    *bool             `json:"granted,omitempty"`
	ProjectKey string            `json:"projectKey"`
	SessionID  string            `json:"sessionId,omitempty"`
}

func (a *App) ledgerSetScope(positional []string, enabled bool, mode ledger.AttachMode) (*ledgerScopeAnswer, error) {
	ctx := context.Background()
	key, err := ledgerProjectKey(a)
	if err != nil {
		return nil, err
	}
	req := map[string]any{"projectKey": key, "enabled": enabled}
	if len(positional) > 1 {
		session, _, err := a.resolveTarget(ctx, positional[1])
		if err != nil {
			return nil, err
		}
		req["sessionId"] = session.ID
	} else if enabled {
		req["attach"] = mode
	}
	var answer ledgerScopeAnswer
	if err := a.ledgerPost(ctx, "/api/v1/ledger/scope", req, &answer); err != nil {
		return nil, err
	}
	return &answer, nil
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
	if item, ok := board.WorkItem(id); ok {
		return a.ledgerWorkShow(store, w, item.ID)
	}
	for _, ask := range board.AllAsks() {
		if ask.ID == id {
			return a.ledgerAsk(store, id, args)
		}
	}
	return fmt.Errorf("%q is not a work item or an ask in this project; agenthail ledger list shows the items", id)
}

func notCarriedLine(refusal registry.ProjectMoveRefusal) string {
	return fmt.Sprintf("history under %s was not carried to %s when the project's remote changed: %s", refusal.From, refusal.To, refusal.Reason)
}
