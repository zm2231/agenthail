package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

const threadMoveUsage = `usage: agenthail thread move <target> --account <name|email|dir> [--wait <duration>] [--stop-terminal] [--json]
       agenthail thread move --all --account <name|email|dir> [--project <name|id>] [--stop-terminal] [--json]
       agenthail thread move --prune [--json]
       agenthail thread adopt <target> [--stop-terminal] [--json]`

type threadMoveOutcome struct {
	Session string              `json:"session"`
	Name    string              `json:"name,omitempty"`
	Result  *surface.MoveResult `json:"result,omitempty"`
	Skipped string              `json:"skipped,omitempty"`
	Error   string              `json:"error,omitempty"`
	busy    *surface.MoveBusyError
}

func (a *App) cmdThreadMove(args []string) error {
	if err := validateThreadMoveArgs(args); err != nil {
		return err
	}
	jsonOut := hasFlag(args, "--json")
	adopt := args[0] == "adopt"
	positional := stripFlags(args)
	if hasFlag(args, "--prune") {
		if adopt || len(positional) != 1 || hasFlag(args, "--all") || flagVal(args, "--account") != "" {
			return errors.New(threadMoveUsage)
		}
		return a.pruneClaudeMoves(jsonOut)
	}
	account := strings.TrimSpace(flagVal(args, "--account"))
	if adopt && account != "" || !adopt && account == "" {
		return errors.New(threadMoveUsage)
	}
	request := surface.MoveRequest{To: surface.MoveTarget{Account: account}, StopTerminal: hasFlag(args, "--stop-terminal")}
	if wait := flagVal(args, "--wait"); wait != "" {
		duration, err := time.ParseDuration(wait)
		if err != nil || duration < 0 {
			return fmt.Errorf("--wait must be a duration such as 5m")
		}
		request.Wait = duration
	}
	timeout, err := commandTimeout(args, a.DefaultTimeout+request.Wait)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if hasFlag(args, "--all") {
		if adopt || len(positional) != 1 {
			return errors.New(threadMoveUsage)
		}
		return a.moveAllClaudeSessions(ctx, request, strings.TrimSpace(flagVal(args, "--project")), jsonOut)
	}
	if len(positional) != 2 || flagVal(args, "--project") != "" {
		return errors.New(threadMoveUsage)
	}
	session, adapter, remote, err := a.resolveAnyTarget(ctx, positional[1])
	if err != nil {
		return err
	}
	if remote != nil {
		return fmt.Errorf("moving a session that runs on another host is not supported yet; run the move on that host")
	}
	outcome := a.moveOne(ctx, adapter, session, request)
	if jsonOut {
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": outcome.Error == "", "move": outcome}); err != nil {
			return err
		}
	} else {
		printThreadMove(outcome)
	}
	if outcome.Error != "" {
		return errors.New(outcome.Error)
	}
	return nil
}

func (a *App) moveOne(ctx context.Context, adapter surface.Surface, session *surface.Session, request surface.MoveRequest) threadMoveOutcome {
	outcome := threadMoveOutcome{Session: session.ID, Name: session.Name}
	mover, ok := adapter.(surface.SessionMover)
	if !ok {
		outcome.Error = fmt.Sprintf("%s sessions cannot be moved", session.Surface)
		return outcome
	}
	result, err := mover.MoveSession(ctx, session, request)
	if err != nil {
		var consent *surface.MoveConsentError
		if errors.As(err, &outcome.busy) {
			outcome.Skipped = outcome.busy.Reason
		} else if errors.As(err, &consent) {
			outcome.Skipped = fmt.Sprintf("open in a terminal (process %d); pass --stop-terminal to stop it", consent.PID)
		}
		outcome.Error = err.Error()
		return outcome
	}
	outcome.Result = result
	if a.registry() != nil && result.Session != nil {
		if err := a.registry().RegisterSession(*result.Session); err != nil {
			outcome.Error = fmt.Sprintf("session %s moved to %s, but its registration failed: %s", session.ID, result.ToRoot, err)
			return outcome
		}
		_ = a.registry().RecordHistory(registry.HistoryEntry{Kind: "session-operation", SessionID: session.ID, Result: "move " + result.FromRoot + " -> " + result.ToRoot})
	}
	return outcome
}

// moveAllClaudeSessions moves every unarchived Claude session of this host that
// is not already in the target root. A busy one is skipped and reported,
// never waited on, so one long turn cannot hold the rest back.
func (a *App) moveAllClaudeSessions(ctx context.Context, request surface.MoveRequest, projectSelector string, jsonOut bool) error {
	catalog, catalogBacked, err := a.listCatalogSnapshot()
	if err != nil {
		return err
	}
	if !catalogBacked {
		return fmt.Errorf("thread move --all needs the daemon; start it with: agenthail daemon start")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	target, err := surface.ResolveClaudeAccountRoot(home, request.To.Account)
	if err != nil {
		return err
	}
	views := map[string]listCatalogView{}
	var sessions []surface.Session
	for _, record := range catalog.Sessions {
		if record.HostID != "" || record.Session.Surface != surface.KindClaude || record.Session.Subagent != nil {
			continue
		}
		views[record.Session.ID] = catalogListView(record)
		sessions = append(sessions, record.Session)
	}
	projectID := ""
	if projectSelector != "" {
		if projectID, err = a.selectedProject(ctx, views, projectSelector, ""); err != nil {
			return err
		}
	}
	sessions = filterListScope(sessions, views, "all", projectID)
	adapter := a.adapters().ForSurface(surface.KindClaude)
	if adapter == nil {
		return fmt.Errorf("surface claude is not configured")
	}
	request.Wait = 0
	var outcomes []threadMoveOutcome
	failures := 0
	for i := range sessions {
		session := sessions[i]
		if account := views[session.ID].ClaudeAccount; account != nil && account.Root == target {
			continue
		}
		outcome := a.moveOne(ctx, adapter, &session, request)
		if outcome.Error != "" && outcome.Skipped == "" {
			failures++
		}
		outcomes = append(outcomes, outcome)
	}
	if jsonOut {
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": failures == 0, "to": target, "moves": outcomes}); err != nil {
			return err
		}
	} else {
		if len(outcomes) == 0 {
			fmt.Printf("every current Claude session is already in %s\n", target)
		}
		for _, outcome := range outcomes {
			printThreadMove(outcome)
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d of %d moves failed", failures, len(outcomes))
	}
	return nil
}

func (a *App) pruneClaudeMoves(jsonOut bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	pruned, err := surfaces.PruneClaudeMoves(home, 0, time.Now())
	if err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "pruned": pruned})
	}
	fmt.Printf("removed the recovery copies of %d moved sessions\n", len(pruned))
	return nil
}

func printThreadMove(outcome threadMoveOutcome) {
	label := outcome.Session
	if outcome.Name != "" {
		label = outcome.Name + " (" + outcome.Session + ")"
	}
	switch {
	case outcome.Skipped != "":
		fmt.Printf("skipped %s: %s\n", label, outcome.Skipped)
	case outcome.Error != "":
		fmt.Printf("failed %s: %s\n", label, outcome.Error)
	case outcome.Result.Unchanged:
		fmt.Printf("%s already runs in Agenthail under %s\n", label, outcome.Result.ToRoot)
	case outcome.Result.FromRoot == outcome.Result.ToRoot:
		fmt.Printf("opened %s in Agenthail under %s\n", label, outcome.Result.ToRoot)
	default:
		fmt.Printf("moved %s from %s to %s\n", label, outcome.Result.FromRoot, outcome.Result.ToRoot)
	}
	if outcome.Result != nil {
		if outcome.Result.Stopped != "" {
			fmt.Printf("  stopped the %s that held it\n", outcome.Result.Stopped)
		}
		for _, warning := range outcome.Result.Warnings {
			fmt.Printf("  warning: %s\n", warning)
		}
	}
}

func validateThreadMoveArgs(args []string) error {
	values := map[string]bool{"--account": true, "--wait": true, "--project": true, "--timeout": true}
	bools := map[string]bool{"--json": true, "--all": true, "--prune": true, "--stop-terminal": true}
	for i := 1; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "--") {
			continue
		}
		switch {
		case values[args[i]]:
			i++
		case bools[args[i]]:
		default:
			return fmt.Errorf("thread %s does not accept %s\n%s", args[0], args[i], threadMoveUsage)
		}
	}
	return nil
}
