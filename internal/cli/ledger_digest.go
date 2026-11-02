package cli

import (
	"fmt"
	"strconv"

	"github.com/zm2231/agenthail/internal/ledger"
)

const ledgerDigestUsage = `usage: agenthail ledger digest [--project <key>] [--through <owner position>] [--json]`

func (a *App) ledgerDigest(store *ledger.Store, args []string) error {
	w, err := parseLedgerWrite(args)
	if err != nil {
		return err
	}
	if len(w.positional) > 1 {
		return fmt.Errorf("%s", ledgerDigestUsage)
	}
	key := w.value("--project")
	if key == "" {
		if key, err = ledgerProjectKey(a); err != nil {
			return err
		}
	}
	through, err := store.DigestThrough(key)
	if err != nil {
		return err
	}
	if raw := w.value("--through"); raw != "" {
		if through, err = strconv.ParseInt(raw, 10, 64); err != nil || through < 0 {
			return fmt.Errorf("--through takes an owner position, a whole number")
		}
	}
	digest, err := store.LogDigest(key, through)
	if err != nil {
		return err
	}
	if w.bools["--json"] {
		return emit(struct {
			ProjectKey string `json:"projectKey"`
			ledger.LogDigest
		}{key, digest})
	}
	fmt.Printf("%s: %d events through owner position %d, sha256 %s\n", key, digest.Count, digest.Through, digest.SHA256)
	return nil
}
