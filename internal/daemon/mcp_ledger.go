package daemon

import (
	"context"
	"errors"

	"github.com/zm2231/agenthail/internal/ledger"
)

type mcpLedgerInput struct {
	View   string `json:"view,omitempty" jsonschema:"board (capture, sync, counts, goals and the newest unattached words; the default), goals, asks, decisions, questions, work, ready (the next work, ranked), or item (one work item or ask with its context, by id)"`
	ID     string `json:"id,omitempty" jsonschema:"the work item, work action or ask id, for the item view"`
	Limit  int    `json:"limit,omitempty" jsonschema:"entries per page, 1 to 100 (ready: 1 to 50); default 25"`
	Cursor string `json:"cursor,omitempty" jsonschema:"nextCursor from a previous call"`
}

func (d *Daemon) mcpLedgerRead(_ context.Context, caller *mcpCaller, input mcpLedgerInput) (any, error) {
	if !caller.has("ledger.read") {
		return nil, refuse("capability_missing", "Reading the ledger needs ledger.read.")
	}
	key, err := LedgerKey(d.Registry, caller.project)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, refuse("invalid", "Your session is not in a project with a ledger; the person adds one with agenthail project add.")
	}
	result, err := d.ledgerRead(key, ledger.ReadRequest{View: input.View, ID: input.ID, Limit: input.Limit, Cursor: input.Cursor, Budget: mcpPageBudget})
	var refusal *ledger.Refusal
	var missing *ledger.NotFound
	switch {
	case errors.As(err, &refusal):
		return nil, refuse("invalid", refusal.Reason+".")
	case errors.As(err, &missing):
		return nil, refuse("not_found", missing.Error()+".")
	}
	return result, err
}
