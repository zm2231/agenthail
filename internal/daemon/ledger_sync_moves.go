package daemon

import (
	"context"
	"net/http"

	"github.com/zm2231/agenthail/internal/registry"
)

type ledgerMovesRequest struct {
	Moves []registry.ProjectMove `json:"moves"`
}

// A host that saw its project move first hands the move to the ledger's
// owner before confirming the new key there, so the owner never starts an
// empty ledger under it.
func (d *Daemon) handOffProjectMoves(ctx context.Context, host *pairedHost, key string) error {
	moves, err := d.sharedProjectMoves()
	if err != nil {
		return err
	}
	var into []registry.ProjectMove
	for _, move := range moves {
		if move.To == key {
			into = append(into, move)
		}
	}
	if len(into) == 0 {
		return nil
	}
	return d.ledgerHostCall(ctx, host, http.MethodPost, "/api/v1/ledger/sync/moves", nil, ledgerMovesRequest{Moves: into}, nil)
}

func (d *Daemon) apiLedgerSyncMovesHandler(w http.ResponseWriter, r *http.Request) {
	var req ledgerMovesRequest
	if !decodeLedgerWrite(w, r, &req) {
		return
	}
	outcomes, err := d.adoptProjectMoves(req.Moves)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "ledger_write_failed", err.Error())
		return
	}
	for _, outcome := range outcomes {
		if outcome.Refused != nil {
			writeAPIError(w, http.StatusConflict, "refused", d.hostIdentity().Name+" did not move "+outcome.Refused.From+" to "+outcome.Refused.To+": "+outcome.Refused.Reason)
			return
		}
	}
	writeDashboardJSON(w, http.StatusOK, struct{}{})
}
