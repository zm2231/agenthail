package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/ledger"
)

const ledgerProofUsage = `usage: agenthail ledger proof <item_id>   (lists the item's proof)
       agenthail ledger proof <item_id> --title "<what was checked>" --expected "<what should happen>"
                             [--observed "<what happened>"] [--outcome pass|fail|pending] [--revision <commit>]
                             [--link <title>=<url>]... [--operation <id>]
       agenthail ledger proof <item_id> --step <id> --base <version> <the same fields>   (replaces the step)
       agenthail ledger proof <item_id> --remove --step <id> --base <version>
       --base is the step's version as you last read it; a newer one is refused, so read it again.
       --operation names one write so a retry records it once; one is made for each call by default.`

func (a *App) ledgerProof(store *ledger.Store, args []string) error {
	w, err := parseLedgerWrite(args)
	if err != nil {
		return err
	}
	if len(w.positional) != 2 {
		return fmt.Errorf("%s", ledgerProofUsage)
	}
	itemID := w.positional[1]
	step, remove := w.value("--step"), w.bools["--remove"]
	if !remove && step == "" && w.value("--title") == "" && w.value("--expected") == "" {
		board, err := a.workBoard(store, w)
		if err != nil {
			return err
		}
		item, ok := board.WorkItem(itemID)
		if !ok {
			return fmt.Errorf("%q is not a work item in this project", itemID)
		}
		if w.bools["--json"] {
			return emit(map[string]any{"itemId": item.ID, "proof": item.Proof, "counts": item.ProofCounts()})
		}
		printProof(item)
		return nil
	}
	write := ledger.ProofWrite{Op: ledger.ProofAdd, OperationID: w.value("--operation")}
	if write.OperationID == "" {
		write.OperationID = uuid.NewString()
	}
	if step != "" || remove {
		if step == "" || w.value("--base") == "" {
			return fmt.Errorf("replacing or removing a step needs --step <id> and --base <version>\n%s", ledgerProofUsage)
		}
		if write.Base, err = strconv.Atoi(w.value("--base")); err != nil {
			return fmt.Errorf("--base is the step's version, a number: %w", err)
		}
		write.Op, write.Step.ID = ledger.ProofReplace, step
		if remove {
			write.Op = ledger.ProofRemove
		}
	}
	if !remove {
		write.Step.Title, write.Step.Expected, write.Step.Observed = w.value("--title"), w.value("--expected"), w.value("--observed")
		write.Step.Outcome, write.Step.Revision = w.value("--outcome"), w.value("--revision")
		for _, pair := range w.values["--link"] {
			title, link, ok := strings.Cut(pair, "=")
			if !ok {
				return fmt.Errorf("--link takes <title>=<url>, got %q", pair)
			}
			write.Step.Attachments = append(write.Step.Attachments, ledger.ProofLink{Title: title, URL: link})
		}
	}
	req := ledger.WorkRequest{Action: ledger.WorkProof, ItemID: itemID, Proof: &write}
	ctx := context.Background()
	if req.ProjectKey, req.SessionID, err = a.ledgerWriteContext(ctx, w); err != nil {
		return err
	}
	var result ledger.WorkResult
	if err := a.ledgerPost(ctx, "/api/v1/ledger/work", req, &result); err != nil {
		return err
	}
	if w.bools["--json"] {
		return emit(result)
	}
	fmt.Println(result.Readback)
	return nil
}

func printProof(item ledger.WorkItem) {
	if len(item.Proof) == 0 {
		fmt.Printf("%s has no proof yet\n", item.ID)
		return
	}
	counts := item.ProofCounts()
	fmt.Printf("%s proof: %d passed, %d failed, %d pending\n", item.ID, counts.Pass, counts.Fail, counts.Pending)
	for _, step := range item.Proof {
		stale := ""
		if step.Stale {
			stale = " (stale: written before the item was sent back or changed hands)"
		}
		fmt.Printf("%s v%d [%s] %s%s\n  expected: %s\n", step.ID, step.Version, step.Outcome, step.Title, stale, step.Expected)
		if step.Observed != "" {
			fmt.Printf("  observed: %s\n", step.Observed)
		}
		if step.Revision != "" {
			fmt.Printf("  at: %s\n", step.Revision)
		}
		for _, link := range step.Attachments {
			fmt.Printf("  %s: %s\n", link.Title, link.URL)
		}
	}
}
