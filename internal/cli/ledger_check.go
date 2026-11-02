package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/ledger"
)

const ledgerCheckUsage = `usage: agenthail ledger check <goal_id>
       agenthail ledger check-report <run> --from <checker session> (--none | --missing "<breadth>" --in <utterance_id> ...)`

func (a *App) ledgerCheck(args []string) error {
	w, err := parseLedgerWrite(args)
	if err != nil {
		return err
	}
	if len(w.positional) != 2 {
		return fmt.Errorf("%s", ledgerCheckUsage)
	}
	ctx := context.Background()
	req := map[string]string{"goalId": w.positional[1]}
	if req["projectKey"], req["sessionId"], err = a.ledgerWriteContext(ctx, w); err != nil {
		return err
	}
	var result struct {
		GoalID string `json:"goalId"`
		Queued bool   `json:"queued"`
	}
	if err := a.ledgerPost(ctx, "/api/v1/ledger/check", req, &result); err != nil {
		return err
	}
	if w.bools["--json"] {
		return emit(result)
	}
	if result.Queued {
		fmt.Printf("intent ledger: %s queued for a narrowing check\n", result.GoalID)
	} else {
		fmt.Printf("intent ledger: %s is already being checked or queued\n", result.GoalID)
	}
	return nil
}

func (a *App) ledgerCheckReport(args []string) error {
	w, err := parseLedgerWrite(args)
	if err != nil {
		return err
	}
	missing, from := w.values["--missing"], w.values["--in"]
	if len(w.positional) != 2 || w.value("--from") == "" || len(missing) != len(from) || w.bools["--none"] == (len(missing) > 0) {
		return fmt.Errorf("%s", ledgerCheckUsage)
	}
	req := ledger.CheckReport{Run: w.positional[1], SessionID: w.value("--from"), None: w.bools["--none"]}
	for i, breadth := range missing {
		req.Missing = append(req.Missing, ledger.Omission{Breadth: breadth, From: from[i]})
	}
	var outcome ledger.CheckOutcome
	if err := a.ledgerPost(context.Background(), "/api/v1/ledger/check-report", req, &outcome); err != nil {
		return err
	}
	if w.bools["--json"] {
		return emit(outcome)
	}
	if len(outcome.Cuts) == 0 {
		fmt.Printf("intent ledger: %s checked, nothing missing\n", outcome.GoalID)
		return nil
	}
	fmt.Printf("intent ledger: %s checked, pending cuts %s\n", outcome.GoalID, strings.Join(outcome.Cuts, ", "))
	return nil
}
