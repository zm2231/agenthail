package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/ledger"
)

const ledgerPhasesUsage = `usage: agenthail ledger phases [--label <phase>=<name>]... [--json]
       with no --label, prints each phase key and the project's name for it; an empty name restores the default
       phases: scope, build, review, verify, check, ship`

func (a *App) ledgerPhases(store *ledger.Store, args []string) error {
	w, err := parseLedgerWrite(args)
	if err != nil {
		return err
	}
	if len(w.positional) != 1 {
		return fmt.Errorf("%s", ledgerPhasesUsage)
	}
	labels := w.values["--label"]
	if len(labels) == 0 {
		board, err := a.workBoard(store, w)
		if err != nil {
			return err
		}
		return printPhaseLabels(board.PhaseLabels(), w.bools["--json"])
	}
	req := ledger.PhaseLabelsRequest{Labels: map[string]string{}}
	for _, pair := range labels {
		key, label, ok := strings.Cut(pair, "=")
		if !ok {
			return fmt.Errorf("--label takes <phase>=<name>, got %q\n%s", pair, ledgerPhasesUsage)
		}
		req.Labels[strings.TrimSpace(key)] = label
	}
	ctx := context.Background()
	if req.ProjectKey, req.SessionID, err = a.ledgerWriteContext(ctx, w); err != nil {
		return err
	}
	var answer struct {
		PhaseLabels map[ledger.Phase]string `json:"phaseLabels"`
	}
	if err := a.ledgerPost(ctx, "/api/v1/ledger/phases", req, &answer); err != nil {
		return err
	}
	return printPhaseLabels(answer.PhaseLabels, w.bools["--json"])
}

func printPhaseLabels(labels map[ledger.Phase]string, asJSON bool) error {
	if asJSON {
		return emit(map[string]any{"phaseLabels": labels})
	}
	for _, phase := range ledger.Phases {
		fmt.Printf("%s: %s\n", phase, labels[phase])
	}
	return nil
}
