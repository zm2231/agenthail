package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func (a *App) ledgerClause(action ledger.ClauseAction, args []string) error {
	w, err := parseLedgerWrite(args)
	if err != nil {
		return err
	}
	if len(w.positional) != 2 {
		return fmt.Errorf(`usage: agenthail ledger own <clause_id> [--session <id>]
       agenthail ledger progress <clause_id> --ref <commit|file|PR|evidence>
       agenthail ledger wait <clause_id> --condition "..." --deadline <30m|4h|2d>`)
	}
	ctx := context.Background()
	req := ledger.ClauseRequest{Action: action, ClauseID: w.positional[1], Ref: w.value("--ref"), Condition: w.value("--condition")}
	if action == ledger.ClauseWait {
		deadline, err := parseLedgerDeadline(w.value("--deadline"), time.Now())
		if err != nil {
			return err
		}
		req.DeadlineMs = deadline.UnixMilli()
	}
	if owner := w.value("--session"); owner != "" {
		session, _, err := a.resolveTarget(ctx, owner)
		if err != nil {
			return err
		}
		req.Owner = session.ID
	}
	if req.ProjectKey, req.SessionID, err = a.ledgerWriteContext(ctx, w); err != nil {
		return err
	}
	var result ledger.ClauseResult
	if err := a.ledgerPost(ctx, "/api/v1/ledger/clause", req, &result); err != nil {
		return err
	}
	if w.bools["--json"] {
		return emit(result)
	}
	fmt.Println(result.Readback)
	return nil
}
