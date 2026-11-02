package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
)

func (a *App) cmdLedger(args []string) error {
	positional := stripFlags(args)
	r := a.registry()
	store := ledger.NewStore(r.WriteDB(), r.ReadDB())

	if len(positional) == 0 {
		return a.ledgerBoard(store, args)
	}
	switch positional[0] {
	case "asks":
		return a.ledgerAsks(store, args)
	case "ask":
		if len(positional) < 2 {
			return fmt.Errorf("usage: agenthail ledger ask <ask_id>")
		}
		return a.ledgerAsk(store, positional[1], args)
	case "enable":
		return a.ledgerEnable(store, args)
	case "disable":
		return a.ledgerDisable(store, args)
	case "goal":
		return a.ledgerGoal(args)
	case "goals":
		return a.ledgerGoals(store, args)
	case "attach":
		return a.ledgerAttach(args, false)
	case "propose":
		return a.ledgerAttach(args, true)
	default:
		return fmt.Errorf("unknown ledger command %q", positional[0])
	}
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
	capture, coverage := "coordinator", "capturing the coordinator's prompts only; enable with: agenthail ledger enable"
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

	if hasFlag(args, "--json") {
		out := map[string]any{
			"projectKey":         key,
			"capture":            capture,
			"goals":              view.goals,
			"asks":               view.asks,
			"decisions":          view.decisions,
			"projectConstraints": view.constraints,
			"unattached":         attachment.Unattached,
			"attachedElsewhere":  attachment.Elsewhere,
		}
		if scope.State == ledger.LedgerEnabled {
			out["captureSince"] = scope.Since
		}
		return emit(out)
	}

	fmt.Println(coverage)
	view.print(attachment)
	return nil
}

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
