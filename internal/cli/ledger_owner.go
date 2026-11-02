package cli

import (
	"context"
	"fmt"

	"github.com/zm2231/agenthail/internal/ledger"
)

const ledgerOwnerUsage = `usage: agenthail ledger owner [<host>] [--project <key>]`

type ledgerOwnerRequest struct {
	ProjectKey string `json:"projectKey"`
	Host       string `json:"host"`
}

type ledgerOwnerResult struct {
	ProjectKey string `json:"projectKey"`
	Owner      string `json:"owner"`
	OwnerName  string `json:"ownerName"`
	Adopted    bool   `json:"adopted"`
	Confirmed  bool   `json:"confirmed"`
	Pending    string `json:"pending,omitempty"`
}

func (a *App) ledgerOwner(store *ledger.Store, args []string) error {
	w, err := parseLedgerWrite(args)
	if err != nil {
		return err
	}
	if len(w.positional) > 2 {
		return fmt.Errorf("%s", ledgerOwnerUsage)
	}
	key := w.value("--project")
	if key == "" {
		if key, err = ledgerProjectKey(a); err != nil {
			return err
		}
	}
	if len(w.positional) == 1 {
		status, err := store.Status(key)
		if err != nil {
			return err
		}
		if w.bools["--json"] {
			return emit(status)
		}
		if status.Owner == "" {
			fmt.Printf("%s: this host keeps the ledger; no other host syncs it\n", key)
			return nil
		}
		fmt.Printf("%s: owner %s, pulled through owner seq %d, %d local events waiting to replay\n", key, status.Owner, status.Cursor, status.Unreplayed)
		return nil
	}
	var result ledgerOwnerResult
	if err := a.ledgerPost(context.Background(), "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: key, Host: w.positional[1]}, &result); err != nil {
		return err
	}
	if w.bools["--json"] {
		return emit(result)
	}
	line := fmt.Sprintf("%s: the ledger is now kept on %s; writes here go through it and its log syncs back", key, result.OwnerName)
	if result.Adopted {
		line += "; this host's existing ledger history moves to it"
	}
	if !result.Confirmed {
		line = fmt.Sprintf("%s: assigned to %s here, but %s has not confirmed it yet (%s); sync confirms it once %s answers, and agenthail doctor shows the state", key, result.OwnerName, result.OwnerName, result.Pending, result.OwnerName)
	}
	fmt.Println(line)
	return nil
}
